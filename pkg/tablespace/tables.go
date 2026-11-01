package tablespace

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTableNotFound is returned for a table that no longer exists.
var ErrTableNotFound = errors.New("table not found")

// userRelations picks the tables, views, materialized views, partitioned and foreign tables
// outside the system schemas.
const userRelations = `
	c.relkind in ('r', 'p', 'v', 'm', 'f')
	and n.nspname not in ('pg_catalog', 'information_schema')
	and n.nspname not like 'pg_toast%'
	and n.nspname not like 'pg_temp%'`

// partitionSum is the size fn gives a relation, or for a partitioned table the sum of the sizes of
// its partitions, since the table holds nothing itself.
func partitionSum(fn string) string {
	return "case when c.relkind = 'p' then coalesce((select sum(" + fn + "(p.relid))::bigint from pg_partition_tree(c.oid) p where p.isleaf), 0) else " + fn + "(c.oid) end"
}

var tableColumns = `
	select c.oid,
	       n.nspname,
	       c.relname,
	       c.relkind,
	       c.relispartition,
	       pg_get_userbyid(c.relowner),
	       coalesce(obj_description(c.oid, 'pg_class'), ''),
	       c.reltuples::bigint,
	       coalesce(s.n_live_tup, 0),
	       coalesce(s.n_dead_tup, 0),
	       ` + partitionSum("pg_total_relation_size") + `,
	       ` + partitionSum("pg_relation_size") + `,
	       ` + partitionSum("pg_indexes_size") + `,
	       coalesce(s.seq_scan, 0),
	       coalesce(s.idx_scan, 0),
	       coalesce(s.n_tup_ins, 0),
	       coalesce(s.n_tup_upd, 0),
	       coalesce(s.n_tup_del, 0),
	       greatest(s.last_vacuum, s.last_autovacuum),
	       greatest(s.last_analyze, s.last_autoanalyze)
	from pg_class c
	join pg_namespace n on n.oid = c.relnamespace
	left join pg_stat_all_tables s on s.relid = c.oid`

// Stats reports whether the server keeps statistics of the table's rows: scans, writes, dead
// rows and when it was vacuumed. A view, a partitioned table and a foreign table have none.
func (t Table) Stats() bool {
	return t.Kind == KindTable || t.Kind == KindMaterialized
}

func scanTable(row pgx.CollectableRow) (Table, error) {
	var t Table
	var kind string
	var vacuum, analyze *time.Time
	err := row.Scan(&t.OID, &t.Schema, &t.Name, &kind, &t.Partition, &t.Owner, &t.Comment,
		&t.Estimate, &t.LiveRows, &t.DeadRows, &t.TotalBytes, &t.TableBytes, &t.IndexBytes,
		&t.SeqScans, &t.IdxScans, &t.Inserted, &t.Updated, &t.Deleted, &vacuum, &analyze)
	t.Kind = relKind(kind)
	if vacuum != nil {
		t.LastVacuum = *vacuum
	}
	if analyze != nil {
		t.LastAnalyze = *analyze
	}
	return t, err
}

func relKind(kind string) string {
	switch kind {
	case "p":
		return KindPartitioned
	case "v":
		return KindView
	case "m":
		return KindMaterialized
	case "f":
		return KindForeign
	}
	return KindTable
}

// ListTables lists the tables of the database by schema and name.
func ListTables(ctx context.Context, pool *pgxpool.Pool) ([]Table, error) {
	rows, err := pool.Query(ctx, tableColumns+" where "+userRelations+" order by n.nspname, c.relname")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanTable)
}

// GetTable loads the table with oid.
func GetTable(ctx context.Context, pool *pgxpool.Pool, oid uint32) (Table, error) {
	rows, err := pool.Query(ctx, tableColumns+" where c.oid = $1", oid)
	if err != nil {
		return Table{}, err
	}
	t, err := pgx.CollectExactlyOneRow(rows, scanTable)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrTableNotFound
	}
	return t, err
}

// ListColumns lists the columns of the table with oid in their order.
func ListColumns(ctx context.Context, pool *pgxpool.Pool, oid uint32) ([]Column, error) {
	rows, err := pool.Query(ctx, `
		select a.attname,
		       format_type(a.atttypid, a.atttypmod),
		       not a.attnotnull,
		       coalesce(pg_get_expr(d.adbin, d.adrelid), ''),
		       a.attidentity::text,
		       a.attgenerated::text,
		       coalesce(col_description(a.attrelid, a.attnum), '')
		from pg_attribute a
		left join pg_attrdef d on d.adrelid = a.attrelid and d.adnum = a.attnum
		where a.attrelid = $1 and a.attnum > 0 and not a.attisdropped
		order by a.attnum`, oid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Column, error) {
		var c Column
		var identity, generated string
		err := row.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &identity, &generated, &c.Comment)
		switch identity {
		case "a":
			c.Identity = "always"
		case "d":
			c.Identity = "by default"
		}
		c.Generated = generated != ""
		return c, err
	})
}

const indexColumns = `
	select n.nspname,
	       t.relname,
	       i.relname,
	       pg_get_indexdef(x.indexrelid),
	       pg_relation_size(x.indexrelid),
	       coalesce(s.idx_scan, 0),
	       x.indisunique,
	       x.indisprimary,
	       x.indisvalid
	from pg_index x
	join pg_class i on i.oid = x.indexrelid
	join pg_class t on t.oid = x.indrelid
	join pg_namespace n on n.oid = t.relnamespace
	left join pg_stat_all_indexes s on s.indexrelid = x.indexrelid`

func scanIndex(row pgx.CollectableRow) (Index, error) {
	var i Index
	err := row.Scan(&i.Schema, &i.Table, &i.Name, &i.Definition, &i.Bytes, &i.Scans, &i.Unique, &i.Primary, &i.Valid)
	return i, err
}

// ListIndexes lists the indexes of the database, by schema, table and name.
func ListIndexes(ctx context.Context, pool *pgxpool.Pool) ([]Index, error) {
	rows, err := pool.Query(ctx, indexColumns+`
		where n.nspname not in ('pg_catalog', 'information_schema') and n.nspname not like 'pg_toast%'
		order by n.nspname, t.relname, i.relname`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanIndex)
}

// ListTableIndexes lists the indexes of the table with oid, the primary key first.
func ListTableIndexes(ctx context.Context, pool *pgxpool.Pool, oid uint32) ([]Index, error) {
	rows, err := pool.Query(ctx, indexColumns+" where x.indrelid = $1 order by x.indisprimary desc, i.relname", oid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanIndex)
}

// ListConstraints lists the constraints of the table with oid, by kind and name. Not null
// constraints are left out, the columns already say which may be null.
func ListConstraints(ctx context.Context, pool *pgxpool.Pool, oid uint32) ([]Constraint, error) {
	rows, err := pool.Query(ctx, `
		select conname,
		       contype::text,
		       pg_get_constraintdef(oid),
		       case when confrelid <> 0 then confrelid::regclass::text else '' end
		from pg_constraint
		where conrelid = $1 and contype <> 'n'
		order by array_position(array['p', 'u', 'f', 'c', 'x'], contype::text), conname`, oid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Constraint, error) {
		var c Constraint
		var kind string
		err := row.Scan(&c.Name, &kind, &c.Definition, &c.References)
		c.Kind = map[string]string{"p": "primary key", "u": "unique", "f": "foreign key", "c": "check", "x": "exclude", "t": "trigger"}[kind]
		return c, err
	})
}

// TruncateTable removes every row of the table.
func TruncateTable(ctx context.Context, pool *pgxpool.Pool, t Table) error {
	_, err := pool.Exec(ctx, "truncate table "+t.QualifiedName())
	return err
}

// DropTable drops the table, view or materialized view. What depends on it makes the server
// refuse, nothing is dropped along with it.
func DropTable(ctx context.Context, pool *pgxpool.Pool, t Table) error {
	what := "table"
	switch t.Kind {
	case KindView:
		what = "view"
	case KindMaterialized:
		what = "materialized view"
	case KindForeign:
		what = "foreign table"
	}
	_, err := pool.Exec(ctx, "drop "+what+" "+t.QualifiedName())
	return err
}
