package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/pgui/pkg/client"
	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/messages"
)

// raceEnabled leaves the values of megabytes out of the sweep, which takes minutes under the race
// detector.
var raceEnabled bool

// layoutSizes are the terminal sizes every page is drawn at: below the minimum, at it, and above.
var layoutSizes = [][2]int{
	{20, 5}, {40, 12}, {60, 20}, {80, 24}, {101, 40}, {160, 24},
	{minContentWidth + chromeWidth, minContentHeight + chromeHeight},
	{102, 26}, {120, 40}, {250, 70},
}

var nastyName = strings.Repeat("very_long_identifier_", 6) + "日本語🚀"

func nastyDatabases() []pgui.Database {
	return []pgui.Database{
		{Name: nastyName, Owner: nastyName, Encoding: "UTF8", Collation: "en_US.UTF-8", Size: 1 << 50, SizeKnown: true, Connections: 123456, AllowConn: true, Comment: strings.Repeat("comment ", 40)},
		{Name: "tab\there\x1b[31m", Owner: "x", AllowConn: true},
		{Name: "template0", Template: true},
	}
}

func nastyBackends() []pgui.Backend {
	long := time.Now().Add(-49 * time.Hour)
	return []pgui.Backend{
		{PID: 2147483647, User: nastyName, Database: nastyName, Application: nastyName, Client: "[2001:db8::1]:65535", State: "active",
			WaitEventType: "Lock", WaitEvent: "transactionid", Query: "select\n\t" + strings.Repeat("a_column, ", 500) + "\x1b]0;pwned\x07 from t",
			Type: "client backend", BackendStart: long, XactStart: long, QueryStart: long, StateChange: long, BlockedBy: []int{1, 2, 3, 4, 5, 6, 7, 8}},
		{PID: 2, State: "idle in transaction (aborted)", Type: "client backend", StateChange: time.Now()},
		{PID: 3, Type: "autovacuum launcher"},
	}
}

func nastyTables() []pgui.Table {
	return []pgui.Table{
		{OID: 1, Schema: nastyName, Name: nastyName, Kind: pgui.KindTable, Owner: nastyName, Comment: strings.Repeat("c ", 200),
			Estimate: 1 << 60, LiveRows: 1 << 60, DeadRows: 1 << 61, TotalBytes: 1 << 60, TableBytes: 1 << 59, IndexBytes: 1 << 59,
			SeqScans: 1 << 62, IdxScans: 1 << 62, Inserted: 1 << 62, Updated: 1 << 62, Deleted: 1 << 62, LastVacuum: time.Now()},
		{OID: 2, Schema: "s", Name: "v", Kind: pgui.KindView, Estimate: -1},
		{OID: 3, Schema: "s", Name: "p", Kind: pgui.KindTable, Partition: true},
	}
}

// nastyPage returns rows with many columns, nulls, control characters and, but under -short, a
// value of megabytes.
func nastyPage(short bool) pgui.Page {
	var fields []pgui.Field
	for i := range 300 {
		fields = append(fields, pgui.Field{Name: fmt.Sprintf("column_%03d_%s", i, nastyName), Type: 25})
	}
	fields[1].Type, fields[2].Type, fields[3].Type = oidJSONB, 1700, oidBytea
	big := `{"k": "` + strings.Repeat("x", 5000) + `"}`
	if !short {
		big = `{"k": "` + strings.Repeat("漢字 ", 1<<20) + `"}`
	}
	var rows []pgui.Row
	for r := range 150 {
		values := make([]pgui.Value, len(fields))
		for i := range values {
			switch {
			case i%7 == 0:
				values[i] = pgui.Value{Null: true}
			case i == 1:
				values[i] = pgui.Value{Text: big}
			default:
				values[i] = pgui.Value{Text: "line1\n\tline2 \x1b[2J 日本語 🚀 " + strings.Repeat("w", r)}
			}
		}
		rows = append(rows, pgui.Row{Key: []string{fmt.Sprint(r)}, Values: values})
	}
	return pgui.Page{Fields: fields, Rows: rows, Next: []string{"0"}}
}

func layoutModel(t *testing.T, w, h int) model {
	m := newModel("test", "pgui")
	m.info = client.Info{Host: strings.Repeat("host", 30) + ":5432", User: nastyName, Database: nastyName}
	m.server = pgui.Server{Version: "18.3", Database: nastyName, User: nastyName, Replica: true, ReadOnly: true, Connections: 99, MaxConnections: 100, DatabaseSize: 1 << 50, Started: time.Now().Add(-1000 * time.Hour)}
	m.serverOK = true
	m.state.sqlEditor.history = []string{"select 1", strings.Repeat("select x from y; ", 100)}
	m, _ = update(t, m, tea.WindowSizeMsg{Width: w, Height: h},
		messages.DatabasesLoadedMsg{Databases: nastyDatabases()},
		messages.BackendsLoadedMsg{Backends: nastyBackends()})
	return m
}

// layoutPages returns every page with hostile data loaded, ready to render at w×h.
func layoutPages(t *testing.T, w, h int, short bool) map[string]model {
	pages := map[string]model{}
	m := layoutModel(t, w, h)
	pages["databases"] = m
	m.statusMsg = strings.Repeat("a very long status ", 20)
	pages["databases with status"] = m
	m.showHelp = true
	pages["help"] = m
	m.showHelp = false
	m.error = strings.Repeat("an error that goes on and on\n", 60)
	pages["error"] = m

	a := layoutModel(t, w, h)
	a, _ = update(t, a, press("tab"))
	pages["activity"] = a
	b, _ := a.BackendDetailsSwitchPage(nastyBackends()[0])
	pages["backend details"] = b
	s, _ := a.BackendSignalSwitchPage([]int{2147483647, 2, 3}, 2, true, false)
	pages["terminate"] = s

	c, _ := layoutModel(t, w, h).DatabaseCreateSwitchPage()
	pages["create database"] = c
	d := layoutModel(t, w, h)
	d.state.databaseDrop = databaseDropState{names: []string{nastyName, "a", "b", "c", "d", "e", "f", "g", "h", "i"}, hidden: 3}
	d, _ = d.DatabaseDropSwitchPage()
	pages["drop database"] = d

	dd := layoutModel(t, w, h)
	dd.state.databaseDetails.database = nastyName
	dd, _ = dd.DatabaseDetailsSwitchPage()
	dd, _ = update(t, dd, messages.TablesLoadedMsg{Database: nastyName, Tables: nastyTables()})
	pages["tables"] = dd
	ix, _ := update(t, dd, press("tab"), messages.IndexesLoadedMsg{Database: nastyName, Indexes: []pgui.Index{{Schema: nastyName, Table: nastyName, Name: nastyName, Definition: strings.Repeat("def ", 100), Bytes: 1 << 60, Scans: 1 << 62}}})
	pages["indexes"] = ix

	td, _ := dd.TableDetailsSwitchPage(nastyTables()[0])
	cols := []pgui.Column{{Name: nastyName, Type: strings.Repeat("character varying(10485760)", 3), Default: strings.Repeat("nextval('x'::regclass)", 10), Comment: nastyName}}
	td, _ = update(t, td,
		messages.TableLoadedMsg{OID: 1, Table: nastyTables()[0]},
		messages.SchemaLoadedMsg{OID: 1, Columns: cols, Paging: pgui.Paging{Key: []string{nastyName}, Types: []string{"text"}},
			Constraints: []pgui.Constraint{{Name: nastyName, Kind: "foreign key", Definition: strings.Repeat("references x ", 40)}}})
	td, _ = update(t, td, messages.RowsLoadedMsg{OID: 1, Gen: td.rowsGen, Page: nastyPage(short)})
	pages["rows"] = td
	sel, _ := update(t, td, press("space"), press("down"), press("space"))
	pages["rows selected"] = sel
	where, _ := update(t, td, press("w"))
	where = typeText(t, where, strings.Repeat("x = 1 and ", 20))
	pages["rows where"] = where
	for i, name := range []string{"columns", "table indexes", "constraints"} {
		p := td
		for range i + 1 {
			p, _ = update(t, p, press("tab"))
		}
		pages[name] = p
	}
	drop, _ := td.tableDropOpen(false)
	pages["drop table"] = drop
	row, _ := update(t, td, press("enter"))
	pages["row details"] = row
	row2, _ := update(t, row, press("down"))
	pages["row details json"] = row2
	del, _ := update(t, td, press("ctrl+d"))
	pages["delete row"] = del

	e, _ := layoutModel(t, w, h).SQLEditorSwitchPage(strings.Repeat("select 1;\n", 100))
	page := nastyPage(short)
	values := make([][]pgui.Value, len(page.Rows))
	for i, r := range page.Rows {
		values[i] = r.Values
	}
	e, _ = update(t, e, messages.QueryRanMsg{Gen: 0, Result: pgui.QueryResult{
		Statements: []pgui.Statement{{Tag: "SELECT 150"}, {Tag: "INSERT 0 1"}}, Fields: page.Fields, Rows: values, Total: 99999, InTx: true}})
	pages["sql editor"] = e
	hist, _ := update(t, e, press("ctrl+r"))
	pages["sql history"] = hist
	failed := e
	failed.state.sqlEditor.result = &pgui.QueryResult{}
	failed.state.sqlEditor.errText = strings.Repeat("syntax error at or near x\n", 50)
	pages["sql error"] = failed
	return pages
}

func TestLayoutFitsTheTerminal(t *testing.T) {
	t.Cleanup(func() { setLayout(142, 34) })
	short := testing.Short() || raceEnabled
	checked := 0
	for _, size := range layoutSizes {
		w, h := size[0], size[1]
		small := w < minContentWidth+chromeWidth || h < minContentHeight+chromeHeight
		for name, m := range layoutPages(t, w, h, short) {
			setLayout(w, h)
			out := m.render()
			lines := strings.Split(out, "\n")
			if len(lines) > h {
				t.Errorf("%s at %dx%d: %d lines, taller than the terminal", name, w, h, len(lines))
			}
			for i, line := range lines {
				if lw := ansi.StringWidth(line); lw > w {
					t.Errorf("%s at %dx%d: line %d is %d wide:\n%s", name, w, h, i, lw, ansi.Strip(line))
					break
				}
			}
			if got := strings.Contains(ansi.Strip(out), "terminal too small"); got != small {
				t.Errorf("%s at %dx%d: expected the too small notice %v, got %v", name, w, h, small, got)
			}
			if strings.Contains(out, "\x1b]0;") || strings.Contains(out, "\x1b[2J") {
				t.Errorf("%s at %dx%d: a control sequence from the data reached the terminal", name, w, h)
			}
			// The frame clips what does not fit, the pages are drawn to not need it.
			if c := m.content(); !small && (lipgloss.Width(c) > contentWidth || lipgloss.Height(c) > contentHeight) {
				t.Errorf("%s at %dx%d: content is %dx%d, the frame holds %dx%d:\n%s", name, w, h,
					lipgloss.Width(c), lipgloss.Height(c), contentWidth, contentHeight, ansi.Strip(c))
			}
			checked++
		}
	}
	t.Logf("checked %d page and size combinations", checked)
}

// TestTooSmallNotice names the size the terminal has and the size it needs.
func TestTooSmallNotice(t *testing.T) {
	t.Cleanup(func() { setLayout(142, 34) })
	m, _ := update(t, newTestModel(), tea.WindowSizeMsg{Width: 80, Height: 24})
	out := ansi.Strip(m.render())
	for _, want := range []string{"terminal too small", "80×24, needs 102×24", "ctrl+c to quit"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the notice:\n%s", want, out)
		}
	}
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 102, Height: 24})
	if strings.Contains(ansi.Strip(m.render()), "terminal too small") {
		t.Error("expected the layout at the minimum size")
	}
}
