package pgui

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ListDatabases lists the databases of the cluster by name.
func ListDatabases(ctx context.Context, pool *pgxpool.Pool) ([]Database, error) {
	rows, err := pool.Query(ctx, `
		select d.datname,
		       pg_get_userbyid(d.datdba),
		       pg_encoding_to_char(d.encoding),
		       d.datcollate,
		       case when has_database_privilege(d.oid, 'CONNECT') then pg_database_size(d.oid) end,
		       (select count(*) from pg_stat_activity a where a.datid = d.oid),
		       d.datallowconn,
		       d.datistemplate,
		       coalesce(shobj_description(d.oid, 'pg_database'), '')
		from pg_database d
		order by d.datname`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Database, error) {
		var d Database
		var size *int64
		err := row.Scan(&d.Name, &d.Owner, &d.Encoding, &d.Collation, &size, &d.Connections, &d.AllowConn, &d.Template, &d.Comment)
		if size != nil {
			d.Size, d.SizeKnown = uint64(*size), true
		}
		return d, err
	})
}

// DatabaseConfig is what a new database is created with. Empty fields take the server's default.
type DatabaseConfig struct {
	Name     string
	Owner    string
	Template string
}

// CreateDatabase creates a database.
func CreateDatabase(ctx context.Context, pool *pgxpool.Pool, cfg DatabaseConfig) error {
	sql := "create database " + QuoteIdent(cfg.Name)
	if cfg.Owner != "" {
		sql += " owner " + QuoteIdent(cfg.Owner)
	}
	if cfg.Template != "" {
		sql += " template " + QuoteIdent(cfg.Template)
	}
	_, err := pool.Exec(ctx, sql)
	return err
}

// DropDatabases drops the databases by name. A database someone is connected to is refused by
// the server, its sessions are left alone.
func DropDatabases(ctx context.Context, pool *pgxpool.Pool, names []string) ([]string, error) {
	return forEach(names, func(name string) error {
		if _, err := pool.Exec(ctx, "drop database "+QuoteIdent(name)); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	})
}
