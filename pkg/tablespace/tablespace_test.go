package tablespace

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kontrolplane/tablespace/internal/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

const shop = `
	create schema shop;
	create table shop.customers (id bigint generated always as identity primary key, name text not null, email text unique);
	comment on table shop.customers is 'people who order';
	create table shop.orders (
		id bigserial primary key,
		customer_id bigint not null references shop.customers (id),
		total numeric(10, 2) check (total >= 0),
		meta jsonb,
		created timestamptz default now()
	);
	create index orders_customer on shop.orders (customer_id);
	create table shop.lines (order_id bigint, n int, sku text, primary key (order_id, n));
	create table shop.events (kind text, payload jsonb);
	create view shop.big_orders as select * from shop.orders where total > 100;
	create materialized view shop.order_counts as select customer_id, count(*) from shop.orders group by 1;
	insert into shop.customers (name, email) values ('ada', 'ada@example.com'), ('bob', null);
	insert into shop.orders (customer_id, total, meta) select 1 + i % 2, i, jsonb_build_object('n', i) from generate_series(1, 250) i;
	insert into shop.lines select o, n, 'sku-' || n from generate_series(1, 30) o, generate_series(1, 4) n;
	insert into shop.events select 'k' || i, null from generate_series(1, 25) i;
	create table shop.visits (id int, at date, primary key (id, at)) partition by range (at);
	create table shop.visits_1 partition of shop.visits for values from ('2026-01-01') to ('2026-07-01');
	create table shop.visits_2 partition of shop.visits for values from ('2026-07-01') to ('2027-01-01');
	insert into shop.visits select i, date '2026-01-01' + i from generate_series(1, 300) i;
	analyze;
`

func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := pgtest.Database(t, shop)
	return pool
}

func table(t *testing.T, pool *pgxpool.Pool, name string) Table {
	t.Helper()
	tables, err := ListTables(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if tb.DisplayName() == name {
			return tb
		}
	}
	t.Fatalf("no table %s in %v", name, tables)
	return Table{}
}

func TestGetServer(t *testing.T) {
	pool := setup(t)
	s, err := GetServer(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Version, "18") || s.User != "tablespace" || s.MaxConnections == 0 || s.Connections == 0 || s.DatabaseSize == 0 || s.Started.IsZero() {
		t.Errorf("unexpected server %+v", s)
	}
	if s.Replica || s.ReadOnly {
		t.Errorf("expected a writable primary, got %+v", s)
	}
}

func TestDatabases(t *testing.T) {
	ctx := context.Background()
	pool, name := pgtest.Database(t)
	dbs, err := ListDatabases(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(dbs, func(d Database) bool { return d.Name == name })
	if i < 0 || !dbs[i].SizeKnown || dbs[i].Owner != "tablespace" || dbs[i].Connections == 0 {
		t.Fatalf("expected %s with its size and connections, got %+v", name, dbs)
	}
	if !slices.ContainsFunc(dbs, func(d Database) bool { return d.Name == "template0" && d.Template && !d.AllowConn }) {
		t.Error("expected template0 listed as a template")
	}

	if err := CreateDatabase(ctx, pool, DatabaseConfig{Name: name + "_new", Template: "template0"}); err != nil {
		t.Fatal(err)
	}
	dropped, err := DropDatabases(ctx, pool, []string{name + "_new", name + "_missing"})
	if err == nil || !slices.Equal(dropped, []string{name + "_new"}) {
		t.Fatalf("expected one drop and one failure, got %v, %v", dropped, err)
	}
}

func TestTables(t *testing.T) {
	pool := setup(t)
	tables, err := ListTables(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range tables {
		names = append(names, tb.DisplayName()+":"+tb.Kind)
	}
	want := []string{"shop.big_orders:view", "shop.customers:table", "shop.events:table", "shop.lines:table", "shop.order_counts:matview", "shop.orders:table",
		"shop.visits:partitioned", "shop.visits_1:table", "shop.visits_2:table"}
	if !slices.Equal(names, want) {
		t.Fatalf("expected %v, got %v", want, names)
	}
	orders := table(t, pool, "shop.orders")
	if orders.Estimate != 250 || orders.TotalBytes == 0 || orders.IndexBytes == 0 || orders.LastAnalyze.IsZero() {
		t.Errorf("expected the stats of orders, got %+v", orders)
	}
	visits, part := table(t, pool, "shop.visits"), table(t, pool, "shop.visits_1")
	if visits.TotalBytes == 0 || visits.TotalBytes < part.TotalBytes || visits.Stats() || !part.Partition {
		t.Errorf("expected the partitioned table sized by its partitions, got %d of %d", visits.TotalBytes, part.TotalBytes)
	}
	if c := table(t, pool, "shop.customers"); c.Comment != "people who order" {
		t.Errorf("expected the comment, got %q", c.Comment)
	}

	got, err := GetTable(context.Background(), pool, orders.OID)
	if err != nil || got.Name != "orders" {
		t.Fatalf("expected orders, got %+v, %v", got, err)
	}
	if _, err := GetTable(context.Background(), pool, 1); !errors.Is(err, ErrTableNotFound) {
		t.Errorf("expected ErrTableNotFound, got %v", err)
	}
}

func TestColumnsIndexesConstraints(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	customers := table(t, pool, "shop.customers")
	cols, err := ListColumns(ctx, pool, customers.OID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 3 || cols[0].Identity != "always" || cols[0].Nullable || !cols[2].Nullable || cols[1].Type != "text" {
		t.Fatalf("unexpected columns %+v", cols)
	}

	orders := table(t, pool, "shop.orders")
	cols, err = ListColumns(ctx, pool, orders.OID)
	if err != nil {
		t.Fatal(err)
	}
	if cols[0].Default == "" || cols[2].Type != "numeric(10,2)" {
		t.Fatalf("expected the serial default and the numeric type, got %+v", cols)
	}

	idx, err := ListTableIndexes(ctx, pool, orders.OID)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 2 || !idx[0].Primary || idx[1].Name != "orders_customer" || idx[1].Method() != "btree (customer_id)" {
		t.Fatalf("unexpected indexes %+v", idx)
	}
	all, err := ListIndexes(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 4 {
		t.Errorf("expected the indexes of every table, got %d", len(all))
	}

	cons, err := ListConstraints(ctx, pool, orders.OID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, c := range cons {
		kinds = append(kinds, c.Kind)
	}
	if !slices.Equal(kinds, []string{"primary key", "foreign key", "check"}) || cons[1].References != "shop.customers" {
		t.Fatalf("unexpected constraints %+v", cons)
	}
}

func allRows(t *testing.T, pool *pgxpool.Pool, q RowsQuery) ([]Row, int) {
	t.Helper()
	var rows []Row
	pages := 0
	for {
		page, err := ListRows(context.Background(), pool, q)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		rows = append(rows, page.Rows...)
		if page.Next == nil {
			return rows, pages
		}
		q.After = page.Next
	}
}

func TestPagingByPrimaryKey(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	orders := table(t, pool, "shop.orders")
	p, err := GetPaging(ctx, pool, orders)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Key, []string{"id"}) || p.CTID || !p.Deletable() {
		t.Fatalf("expected paging by id, got %+v", p)
	}
	page, err := ListRows(ctx, pool, RowsQuery{Table: orders, Paging: p, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Fields) != 5 || page.Fields[0].Name != "id" || page.Fields[3].Name != "meta" {
		t.Fatalf("expected the columns of the table only, got %+v", page.Fields)
	}
	if page.Rows[0].Values[0].Text != "250" || page.Rows[0].Values[3].Text != `{"n": 250}` || page.Next[0] != "151" {
		t.Fatalf("expected the newest first in text, got %+v next %v", page.Rows[0], page.Next)
	}
	rows, pages := allRows(t, pool, RowsQuery{Table: orders, Paging: p, Limit: 100})
	if len(rows) != 250 || pages != 3 || rows[249].Key[0] != "1" {
		t.Fatalf("expected 250 rows over 3 pages, got %d over %d", len(rows), pages)
	}

	rows, _ = allRows(t, pool, RowsQuery{Table: orders, Paging: p, Limit: 100, Where: "customer_id = 1 -- odd ones"})
	if len(rows) != 125 {
		t.Fatalf("expected the rows of the where clause, got %d", len(rows))
	}
}

func TestPagingByCompositeKey(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	lines := table(t, pool, "shop.lines")
	p, err := GetPaging(ctx, pool, lines)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Key, []string{"order_id", "n"}) {
		t.Fatalf("expected the key in index order, got %+v", p)
	}
	rows, pages := allRows(t, pool, RowsQuery{Table: lines, Paging: p, Limit: 50})
	if len(rows) != 120 || pages != 3 {
		t.Fatalf("expected 120 rows over 3 pages, got %d over %d", len(rows), pages)
	}
	if !slices.Equal(rows[0].Key, []string{"30", "4"}) || !slices.Equal(rows[1].Key, []string{"30", "3"}) {
		t.Fatalf("expected descending keys, got %v %v", rows[0].Key, rows[1].Key)
	}

	n, err := DeleteRows(ctx, pool, lines, p, [][]string{{"30", "4"}, {"30", "3"}, {"99", "9"}})
	if err != nil || n != 2 {
		t.Fatalf("expected 2 deleted, got %d, %v", n, err)
	}
}

func TestPagingByCTIDAndOffset(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	events := table(t, pool, "shop.events")
	p, err := GetPaging(ctx, pool, events)
	if err != nil {
		t.Fatal(err)
	}
	if !p.CTID || p.Deletable() {
		t.Fatalf("expected paging by ctid, got %+v", p)
	}
	rows, pages := allRows(t, pool, RowsQuery{Table: events, Paging: p, Limit: 10})
	if len(rows) != 25 || pages != 3 || rows[0].Values[0].Text != "k25" || !rows[0].Values[1].Null {
		t.Fatalf("expected 25 rows newest first with nulls, got %d over %d: %+v", len(rows), pages, rows[0])
	}
	if _, err := DeleteRows(ctx, pool, events, p, [][]string{rows[0].Key}); err == nil {
		t.Error("expected deleting from a table without a primary key to be refused")
	}

	view := table(t, pool, "shop.big_orders")
	p, err = GetPaging(ctx, pool, view)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ByOffset() {
		t.Fatalf("expected a view paged by offset, got %+v", p)
	}
	rows, pages = allRows(t, pool, RowsQuery{Table: view, Paging: p, Limit: 100})
	if len(rows) != 150 || pages != 2 {
		t.Fatalf("expected 150 rows over 2 pages, got %d over %d", len(rows), pages)
	}
	if _, err := ListRows(ctx, pool, RowsQuery{Table: view, Paging: p, Limit: 10, After: []string{"1; drop table x"}}); err == nil {
		t.Error("expected an offset that is not a number to be refused")
	}
}

func TestTruncateAndDrop(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	if err := TruncateTable(ctx, pool, table(t, pool, "shop.events")); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from shop.events").Scan(&n); err != nil || n != 0 {
		t.Fatalf("expected no rows left, got %d, %v", n, err)
	}
	if err := DropTable(ctx, pool, table(t, pool, "shop.orders")); err == nil {
		t.Error("expected dropping a table a view depends on to be refused")
	}
	for _, name := range []string{"shop.big_orders", "shop.order_counts", "shop.events"} {
		if err := DropTable(ctx, pool, table(t, pool, name)); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	}
}

func TestBackends(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	other, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	done := make(chan error, 1)
	go func() {
		_, err := other.Exec(ctx, "select pg_sleep(30)")
		done <- err
	}()

	var sleeper Backend
	deadline := time.Now().Add(10 * time.Second)
	for sleeper.PID == 0 && time.Now().Before(deadline) {
		backends, err := ListBackends(ctx, pool)
		if err != nil {
			t.Fatal(err)
		}
		if i := slices.IndexFunc(backends, func(b Backend) bool { return strings.Contains(b.Query, "pg_sleep(30)") && b.State == "active" }); i >= 0 {
			sleeper = backends[i]
		}
		if !slices.ContainsFunc(backends, func(b Backend) bool { return b.Self }) {
			t.Fatal("expected the own session to be marked")
		}
	}
	if sleeper.PID == 0 || sleeper.QueryStart.IsZero() || sleeper.Type != "client backend" {
		t.Fatalf("expected the sleeping session, got %+v", sleeper)
	}
	cancelled, err := CancelBackends(ctx, pool, []int{sleeper.PID})
	if err != nil || len(cancelled) != 1 {
		t.Fatalf("expected the query cancelled, got %v, %v", cancelled, err)
	}
	if err := <-done; !IsCanceled(err) {
		t.Errorf("expected the sleep to be cancelled, got %v", err)
	}
	if _, err := TerminateBackends(ctx, pool, []int{999999}); err == nil {
		t.Error("expected terminating a missing pid to fail")
	}
}

func TestSession(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	s := NewSession(pool.Config().ConnConfig)
	defer s.Close()

	res, err := s.Run(ctx, "create table t (n int); insert into t select generate_series(1, 1500); select n, null as nothing from t order by n desc")
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, st := range res.Statements {
		tags = append(tags, st.Tag)
	}
	if !slices.Equal(tags, []string{"CREATE TABLE", "INSERT 0 1500", "SELECT 1500"}) {
		t.Fatalf("unexpected tags %v", tags)
	}
	if len(res.Rows) != MaxResultRows || res.Total != 1500 || !res.Truncated() || res.Rows[0][0].Text != "1500" || !res.Rows[0][1].Null {
		t.Fatalf("expected the first %d of 1500 rows, got %d of %d", MaxResultRows, len(res.Rows), res.Total)
	}

	// A transaction carries over to the next run.
	if res, err = s.Run(ctx, "begin; delete from t"); err != nil || !res.InTx {
		t.Fatalf("expected an open transaction, got %+v, %v", res, err)
	}
	if res, err = s.Run(ctx, "rollback; select count(*) from t"); err != nil || res.InTx || res.Rows[0][0].Text != "1500" {
		t.Fatalf("expected the delete rolled back, got %+v, %v", res, err)
	}

	if _, err := s.Run(ctx, "select 1; select * from missing; select 2"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected the error of the failing statement, got %v", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := s.Run(cctx, "select pg_sleep(30)"); !IsCanceled(err) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the query cancelled, got %v", err)
	}
	if res, err = s.Run(ctx, "select 1"); err != nil || res.Rows[0][0].Text != "1" {
		t.Fatalf("expected the session usable after a cancel, got %+v, %v", res, err)
	}
}

func TestQuoteIdent(t *testing.T) {
	tb := Table{Schema: "My Schema", Name: `we"ird`}
	if got := tb.QualifiedName(); got != `"My Schema"."we""ird"` {
		t.Errorf("unexpected %s", got)
	}
}
