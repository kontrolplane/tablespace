package tablespace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QuoteIdent quotes a name for use in a query.
func QuoteIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}

// GetServer describes the server and the database pool is connected to, and how long a round
// trip to it takes.
func GetServer(ctx context.Context, pool *pgxpool.Pool) (Server, error) {
	var s Server
	start := time.Now()
	if err := pool.Ping(ctx); err != nil {
		return s, err
	}
	s.RTT = time.Since(start)
	err := pool.QueryRow(ctx, `
		select current_setting('server_version'),
		       current_database(),
		       current_user,
		       coalesce((select ssl from pg_stat_ssl where pid = pg_backend_pid()), false),
		       pg_is_in_recovery(),
		       current_setting('default_transaction_read_only') = 'on',
		       (select count(*) from pg_stat_activity where backend_type = 'client backend'),
		       current_setting('max_connections')::int,
		       pg_database_size(current_database()),
		       pg_postmaster_start_time()`).
		Scan(&s.Version, &s.Database, &s.User, &s.SSL, &s.Replica, &s.ReadOnly, &s.Connections, &s.MaxConnections, &s.DatabaseSize, &s.Started)
	// The version can carry the build, like "18.3 (Debian 18.3-1)".
	s.Version, _, _ = strings.Cut(s.Version, " ")
	return s, err
}

// bulkWorkers bounds the concurrent requests of a bulk operation.
const bulkWorkers = 4

// forEach runs fn for every item with bounded parallelism. It returns the items fn succeeded for,
// in their original order, and the joined errors of the ones it failed for.
func forEach[T any](items []T, fn func(T) error) ([]T, error) {
	errs := make([]error, len(items))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(bulkWorkers, len(items)) {
		wg.Go(func() {
			for i := range next {
				errs[i] = fn(items[i])
			}
		})
	}
	for i := range items {
		next <- i
	}
	close(next)
	wg.Wait()

	var done []T
	for i, item := range items {
		if errs[i] == nil {
			done = append(done, item)
		}
	}
	return done, errors.Join(errs...)
}
