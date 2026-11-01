package pgui

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ListBackends lists the sessions and background processes of the server, client sessions
// first, the longest running at the top.
func ListBackends(ctx context.Context, pool *pgxpool.Pool) ([]Backend, error) {
	rows, err := pool.Query(ctx, `
		select pid,
		       coalesce(usename, ''),
		       coalesce(datname, ''),
		       coalesce(application_name, ''),
		       case
		         when client_addr is not null then host(client_addr) || ':' || client_port
		         when client_port = -1 then 'local'
		         else ''
		       end,
		       coalesce(state, ''),
		       coalesce(wait_event_type, ''),
		       coalesce(wait_event, ''),
		       coalesce(query, ''),
		       coalesce(backend_type, ''),
		       backend_start,
		       xact_start,
		       query_start,
		       state_change,
		       pg_blocking_pids(pid),
		       pid = pg_backend_pid()
		from pg_stat_activity
		order by backend_type <> 'client backend', state = 'idle', query_start nulls last, pid`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Backend, error) {
		var b Backend
		var backendStart, xactStart, queryStart, stateChange *time.Time
		err := row.Scan(&b.PID, &b.User, &b.Database, &b.Application, &b.Client, &b.State,
			&b.WaitEventType, &b.WaitEvent, &b.Query, &b.Type,
			&backendStart, &xactStart, &queryStart, &stateChange, &b.BlockedBy, &b.Self)
		for _, t := range []struct {
			dst *time.Time
			src *time.Time
		}{{&b.BackendStart, backendStart}, {&b.XactStart, xactStart}, {&b.QueryStart, queryStart}, {&b.StateChange, stateChange}} {
			if t.src != nil {
				*t.dst = *t.src
			}
		}
		return b, err
	})
}

// CancelBackends cancels the query each backend runs, leaving its session open.
func CancelBackends(ctx context.Context, pool *pgxpool.Pool, pids []int) ([]int, error) {
	return signalBackends(ctx, pool, "pg_cancel_backend", pids)
}

// TerminateBackends ends the sessions of the backends.
func TerminateBackends(ctx context.Context, pool *pgxpool.Pool, pids []int) ([]int, error) {
	return signalBackends(ctx, pool, "pg_terminate_backend", pids)
}

func signalBackends(ctx context.Context, pool *pgxpool.Pool, fn string, pids []int) ([]int, error) {
	return forEach(pids, func(pid int) error {
		var ok bool
		if err := pool.QueryRow(ctx, "select "+fn+"($1)", pid).Scan(&ok); err != nil {
			return fmt.Errorf("pid %d: %w", pid, err)
		}
		if !ok {
			return fmt.Errorf("pid %d: no longer running", pid)
		}
		return nil
	})
}
