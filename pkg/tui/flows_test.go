package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/huh/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kontrolplane/tablespace/pkg/tablespace"
)

// openTable opens the database the model is connected to, then the table by its name.
func openTable(t *testing.T, m model, name string) model {
	t.Helper()
	m = cursorToDatabase(t, m, m.info.Database)
	m = step(t, m, "enter")
	if m.page != databaseDetails || !m.state.databaseDetails.tablesLoaded {
		t.Fatalf("expected the tables of %s, got page %v", m.info.Database, m.page)
	}
	m = step(t, m, "/")
	m = typeText(t, m, name)
	m = step(t, m, "enter", "enter")
	td := m.state.tableDetails
	if m.page != tableDetails || td.table.DisplayName() != name || !td.schemaLoaded || !td.rowsLoaded {
		t.Fatalf("expected the loaded table %s, got page %v table %q", name, m.page, td.table.DisplayName())
	}
	return m
}

func cursorToDatabase(t *testing.T, m model, name string) model {
	t.Helper()
	i := slices.IndexFunc(m.getFilteredDatabases(), func(d tablespace.Database) bool { return d.Name == name })
	if i < 0 {
		t.Fatalf("no database %s listed", name)
	}
	m.state.databaseOverview.selectedDatabase = i
	m.state.databaseOverview.databasesTable.SetCursor(i)
	return m
}

func TestFlowInitLoadsDatabasesAndServer(t *testing.T) {
	m, _ := liveModel(t)
	if m.loading || !m.serverOK || !m.state.databaseOverview.databasesLoaded {
		t.Fatalf("expected the overview loaded, loading %v server %v", m.loading, m.serverOK)
	}
	s := screen(m)
	for _, want := range []string{"postgres 18", m.info.Database, "connections", "● connected"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q on screen:\n%s", want, s)
		}
	}
}

func TestFlowBrowseTableRows(t *testing.T) {
	m, _ := liveModel(t)
	m = openTable(t, m, "shop.orders")
	td := m.state.tableDetails
	if len(td.rows) != 100 || td.next == nil || td.rows[0].Key[0] != "250" {
		t.Fatalf("expected the newest 100 orders, got %d next %v", len(td.rows), td.next)
	}
	if s := screen(m); !strings.Contains(s, "newest first by id") || !strings.Contains(s, "more below") {
		t.Errorf("expected the order and more rows below on screen:\n%s", s)
	}

	// Moving past the last row loads the older pages, until the first order.
	for range 3 {
		m = step(t, m, "G", "down")
	}
	td = m.state.tableDetails
	if len(td.rows) != 250 || td.next != nil || !td.paged || td.rows[249].Key[0] != "1" {
		t.Fatalf("expected all 250 orders, got %d", len(td.rows))
	}

	// A where clause loads the matching rows from the server.
	m = step(t, m, "w")
	m = typeText(t, m, "total < 10")
	m = step(t, m, "enter")
	td = m.state.tableDetails
	if len(td.rows) != 9 || td.where != "total < 10" || td.paged {
		t.Fatalf("expected the 9 orders below 10, got %d", len(td.rows))
	}
	if s := screen(m); !strings.Contains(s, "where total < 10") {
		t.Errorf("expected the where clause in the frame:\n%s", s)
	}

	// A broken clause says why and keeps the page.
	m = step(t, m, "w", "ctrl+u")
	m = typeText(t, m, "nope = 1")
	m = step(t, m, "enter")
	if !strings.Contains(m.error, `column "nope" does not exist`) || m.page != tableDetails {
		t.Fatalf("expected the server's error, got %q", m.error)
	}
}

func TestFlowOpenRowAndScrollColumns(t *testing.T) {
	m, _ := liveModel(t)
	m = openTable(t, m, "shop.orders")
	m = step(t, m, "enter")
	if m.page != rowDetails || m.state.rowDetails.title() != "250" {
		t.Fatalf("expected the details of order 250, got page %v %q", m.page, m.state.rowDetails.title())
	}
	m = step(t, m, "down", "down", "down")
	if s := screen(m); !strings.Contains(s, `"n": 250`) || !strings.Contains(s, "jsonb") {
		t.Errorf("expected the pretty printed jsonb of the row:\n%s", s)
	}
	m = step(t, m, "q")
	if m.page != tableDetails {
		t.Fatalf("expected back on the table, got %v", m.page)
	}
	m = step(t, m, "right")
	if m.state.tableDetails.colOffset != 0 {
		t.Error("expected no scrolling while every column fits")
	}

	m = step(t, m, "q", "q", "q")
	m = openTable(t, m, "shop.wide")
	if s := screen(m); !strings.Contains(s, "columns 1–") || strings.Contains(s, "c12") {
		t.Fatalf("expected the columns past the width hidden:\n%s", s)
	}
	m = step(t, m, "right", "right")
	td := m.state.tableDetails
	if first, _ := td.visibleColumns(); first != 2 || td.rowsTable.columns[0].title != "c2" {
		t.Errorf("expected the columns scrolled by two, got offset %d", td.colOffset)
	}
}

func TestFlowDeleteRows(t *testing.T) {
	ctx := context.Background()
	m, pool := liveModel(t)
	m = openTable(t, m, "shop.orders")
	m = step(t, m, "space", "down", "space", "ctrl+d")
	if m.page != rowDelete || len(m.state.rowDelete.keys) != 2 {
		t.Fatalf("expected to be asked about 2 rows, got page %v", m.page)
	}
	m = step(t, m, "y")
	if m.page != tableDetails || !strings.Contains(m.statusMsg, "deleted 2 rows") {
		t.Fatalf("expected the rows deleted, got page %v status %q error %q", m.page, m.statusMsg, m.error)
	}
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from shop.orders where id in (249, 250)").Scan(&n); err != nil || n != 0 {
		t.Fatalf("expected orders 249 and 250 gone, got %d, %v", n, err)
	}
	if m.state.tableDetails.rows[0].Key[0] != "248" {
		t.Errorf("expected the rows reloaded, got %v", m.state.tableDetails.rows[0].Key)
	}

	// A table without a primary key refuses.
	m = step(t, m, "q", "q", "q")
	m = openTable(t, m, "shop.events")
	m = step(t, m, "ctrl+d")
	if m.page != tableDetails || !strings.Contains(m.statusMsg, "no primary key") {
		t.Errorf("expected the delete refused, got page %v status %q", m.page, m.statusMsg)
	}
}

func TestFlowTruncateAndDropTable(t *testing.T) {
	ctx := context.Background()
	m, pool := liveModel(t)
	m = openTable(t, m, "shop.events")
	m = step(t, m, "ctrl+p")
	if m.page != tableDrop || !m.state.tableDrop.truncate {
		t.Fatalf("expected the truncate dialog, got %v", m.page)
	}
	m = step(t, m, "enter")
	if m.page != tableDrop {
		t.Fatal("expected enter without the name typed to do nothing")
	}
	m = typeText(t, m, "shop.events")
	m = step(t, m, "enter")
	if m.page != tableDetails || len(m.state.tableDetails.rows) != 0 || !strings.Contains(m.statusMsg, "truncated") {
		t.Fatalf("expected the table emptied, got page %v rows %d error %q", m.page, len(m.state.tableDetails.rows), m.error)
	}

	m = step(t, m, "tab", "ctrl+d")
	m = typeText(t, m, "shop.events")
	m = step(t, m, "enter")
	if m.page != databaseDetails || !strings.Contains(m.statusMsg, "dropped table shop.events") {
		t.Fatalf("expected the table dropped, got page %v status %q error %q", m.page, m.statusMsg, m.error)
	}
	if slices.ContainsFunc(m.state.databaseDetails.tables, func(t tablespace.Table) bool { return t.Name == "events" }) {
		t.Error("expected the table gone from the list")
	}
	var exists bool
	if err := pool.QueryRow(ctx, "select to_regclass('shop.events') is not null").Scan(&exists); err != nil || exists {
		t.Errorf("expected shop.events dropped, got %v, %v", exists, err)
	}
}

func TestFlowReadOnlyRefusesChanges(t *testing.T) {
	m, _ := liveModel(t)
	m.info.ReadOnly = true
	m = openTable(t, m, "shop.orders")
	for _, k := range []string{"ctrl+d", "ctrl+p"} {
		m = step(t, m, k)
		if m.page != tableDetails || !strings.Contains(m.statusMsg, "read only") {
			t.Errorf("expected %s refused, got page %v status %q", k, m.page, m.statusMsg)
		}
	}
}

func TestFlowSQLEditor(t *testing.T) {
	m, _ := liveModel(t)
	m = step(t, m, "e")
	if m.page != sqlEditor || !m.textInputActive() {
		t.Fatalf("expected the editor, got %v", m.page)
	}
	m = typeText(t, m, "select id, meta from shop.orders order by id limit 3; select 1 as one")
	m = step(t, m, "ctrl+s")
	e := m.state.sqlEditor
	if e.running || e.result == nil || len(e.result.Statements) != 2 || len(e.result.Rows) != 1 {
		t.Fatalf("expected two statements with the last result shown, got %+v %q", e.result, e.errText)
	}
	if s := screen(m); !strings.Contains(s, "select 3 · select 1") || !strings.Contains(s, "one") {
		t.Errorf("expected the tags and the result on screen:\n%s", s)
	}

	// A failing query shows the error under the editor.
	m = step(t, m, "ctrl+a", "ctrl+k")
	m = typeText(t, m, "select nope")
	m = step(t, m, "ctrl+s")
	if e := m.state.sqlEditor; !strings.Contains(e.errText, `column "nope" does not exist`) {
		t.Fatalf("expected the error, got %q", e.errText)
	}

	// The history holds both, newest first.
	m = step(t, m, "ctrl+r")
	if !m.state.sqlEditor.historyOpen || len(m.state.sqlEditor.history) != 2 {
		t.Fatalf("expected the history, got %v", m.state.sqlEditor.history)
	}
	m = step(t, m, "down", "enter")
	if v := m.state.sqlEditor.input.Value(); !strings.HasPrefix(v, "select id, meta") {
		t.Errorf("expected the older query back in the editor, got %q", v)
	}

	// A transaction stays open across runs, the editor has a session of its own.
	m = step(t, m, "ctrl+a", "ctrl+k")
	m = typeText(t, m, "begin; delete from shop.orders")
	m = step(t, m, "ctrl+s")
	if !m.state.sqlEditor.result.InTx || !strings.Contains(screen(m), "transaction open") {
		t.Fatalf("expected an open transaction, got %+v", m.state.sqlEditor.result)
	}
	m = step(t, m, "ctrl+a", "ctrl+k")
	m = typeText(t, m, "rollback")
	m = step(t, m, "ctrl+s")
	if m.state.sqlEditor.result.InTx {
		t.Error("expected the transaction closed")
	}
}

func TestFlowSQLEditorCancel(t *testing.T) {
	m, _ := liveModel(t)
	m = step(t, m, "e")
	m = typeText(t, m, "select pg_sleep(30)")
	m, cmd := update(t, m, press("ctrl+s"))
	if !m.state.sqlEditor.running {
		t.Fatal("expected the query running")
	}
	done := make(chan *model)
	go func() {
		settled := drain(t, m, cmd)
		done <- &settled
	}()
	time.Sleep(300 * time.Millisecond)
	// esc cancels the context the query runs under, as the update loop does.
	m.state.sqlEditor.cancel()
	select {
	case settled := <-done:
		m = *settled
	case <-time.After(10 * time.Second):
		t.Fatal("expected the query cancelled")
	}
	if e := m.state.sqlEditor; e.running || e.errText != "cancelled" {
		t.Fatalf("expected the run cancelled, got running %v err %q", e.running, e.errText)
	}
}

func TestFlowActivityTerminate(t *testing.T) {
	ctx := context.Background()
	m, pool := liveModel(t)
	other, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	done := make(chan error, 1)
	go func() {
		_, err := other.Exec(ctx, "select pg_sleep(30) /* sleeper */")
		done <- err
	}()

	m = step(t, m, "tab")
	if m.state.databaseOverview.tab != tabActivity || !m.state.databaseOverview.backendsLoaded {
		t.Fatal("expected the activity tab loaded")
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(screen(m), "sleeper") && time.Now().Before(deadline) {
		m = step(t, m, "r")
	}
	m = step(t, m, "/")
	m = typeText(t, m, "sleeper")
	m = step(t, m, "enter", "enter")
	if m.page != backendDetails || !strings.Contains(m.state.backendDetails.backend.Query, "sleeper") {
		t.Fatalf("expected the details of the sleeping backend, got %v", m.page)
	}
	m = step(t, m, "ctrl+d")
	if m.page != backendSignal || !m.state.backendSignal.terminate {
		t.Fatalf("expected the terminate dialog, got %v", m.page)
	}
	m = step(t, m, "y")
	if !strings.Contains(m.statusMsg, "terminated pid") {
		t.Fatalf("expected the backend terminated, got status %q error %q", m.statusMsg, m.error)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected the sleeping query to fail")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("expected the session ended")
	}
}

func TestFlowCreateOpenAndDropDatabase(t *testing.T) {
	m, _ := liveModel(t)
	name := m.info.Database + "_extra"
	m = step(t, m, "ctrl+n")
	if m.page != databaseCreate {
		t.Fatalf("expected the form, got %v", m.page)
	}
	// huh moves between its fields with commands of its own, which collect leaves out, so the
	// form is filled in and completed directly.
	m.state.databaseCreate.input.name = name
	m.state.databaseCreate.form.State = huh.StateCompleted
	m = step(t, m, "enter")
	if m.page != databaseOverview || !strings.Contains(m.statusMsg, "created database "+name) {
		t.Fatalf("expected the database created, got page %v status %q error %q", m.page, m.statusMsg, m.error)
	}
	if d, ok := m.currentDatabase(); !ok || d.Name != name {
		t.Errorf("expected the cursor on the new database, got %+v", d)
	}

	// Opening it connects a pool to it.
	original := m.info.Database
	m = step(t, m, "enter")
	if m.page != databaseDetails || m.info.Database != name || m.state.databaseDetails.database != name || !m.state.databaseDetails.tablesLoaded {
		t.Fatalf("expected connected to %s, got page %v database %q", name, m.page, m.info.Database)
	}
	if m.serverOK && m.server.Database != name {
		t.Errorf("expected the header on %s, got %s", name, m.server.Database)
	}
	m = step(t, m, "q")

	// The database it is connected to cannot be dropped.
	m = cursorToDatabase(t, m, name)
	m = step(t, m, "ctrl+d")
	if m.page != databaseOverview || !strings.Contains(m.statusMsg, "connected to "+name) {
		t.Fatalf("expected the drop refused, got page %v status %q", m.page, m.statusMsg)
	}
	m = cursorToDatabase(t, m, original)
	m = step(t, m, "enter", "q")
	m = cursorToDatabase(t, m, name)
	m = step(t, m, "ctrl+d")
	m = typeText(t, m, name)
	m = step(t, m, "enter")
	if !strings.Contains(m.statusMsg, "dropped database "+name) {
		t.Fatalf("expected the database dropped, got status %q error %q", m.statusMsg, m.error)
	}
	if slices.ContainsFunc(m.state.databaseOverview.databases, func(d tablespace.Database) bool { return d.Name == name }) {
		t.Error("expected the database gone from the list")
	}
}
