package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kontrolplane/pgui/internal/pgtest"
	"github.com/kontrolplane/pgui/pkg/client"
	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/messages"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

func newTestModel() model {
	m := newModel("test", "pgui")
	m.width = 160
	m.height = 50
	m.loading = false
	return m
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	}
	if ctrl, ok := strings.CutPrefix(s, "ctrl+"); ok {
		return tea.KeyPressMsg{Code: rune(ctrl[0]), Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func update(t *testing.T, m model, msgs ...tea.Msg) (model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(model)
	}
	return m, cmd
}

func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		m, _ = update(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func cmdName(cmd tea.Cmd) string {
	return runtime.FuncForPC(reflect.ValueOf(cmd).Pointer()).Name()
}

// cmdFile returns the source file that defines the function behind a command.
func cmdFile(cmd tea.Cmd) string {
	pc := reflect.ValueOf(cmd).Pointer()
	file, _ := runtime.FuncForPC(pc).FileLine(pc)
	return filepath.ToSlash(file)
}

// collect runs the commands that talk to postgres and returns their messages. Timers (refresh
// ticks, status clearing, spinner, cursor blink) and the system clipboard are skipped, which keeps
// the flows fast and deterministic.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	// Closure names depend on what the compiler inlines, so commands are told apart by the file
	// that defines them instead.
	name := cmdName(cmd)
	batch := strings.Contains(name, ".compactCmds[")
	if !batch && (!strings.HasSuffix(cmdFile(cmd), "/pkg/tui/commands/commands.go") || strings.Contains(name, "CopyToClipboard")) {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, c := range b {
			msgs = append(msgs, collect(c)...)
		}
		return msgs
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// drain runs cmd and feeds every resulting message back into the model until it settles.
func drain(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	queue := collect(cmd)
	for i := 0; len(queue) > 0; i++ {
		if i > 100 {
			t.Fatal("model did not settle")
		}
		msg := queue[0]
		queue = queue[1:]
		m, cmd = update(t, m, msg)
		queue = append(queue, collect(cmd)...)
	}
	return m
}

// step presses the keys one by one, draining the commands each one returns.
func step(t *testing.T, m model, keys ...string) model {
	t.Helper()
	for _, k := range keys {
		var cmd tea.Cmd
		m, cmd = update(t, m, press(k))
		m = drain(t, m, cmd)
	}
	return m
}

func screen(m model) string { return ansi.Strip(m.render()) }

var seed = `
	create schema shop;
	create table shop.customers (id bigint generated always as identity primary key, name text not null);
	create table shop.orders (id bigserial primary key, customer_id bigint references shop.customers (id), total numeric(10, 2), meta jsonb);
	create table shop.events (kind text, payload jsonb);
	insert into shop.customers (name) values ('ada'), ('bob');
	insert into shop.orders (customer_id, total, meta) select 1 + i % 2, i, jsonb_build_object('n', i) from generate_series(1, 250) i;
	insert into shop.events select 'k' || i, null from generate_series(1, 5) i;
	create table shop.wide as select i as id, ` + wideColumns + ` from generate_series(1, 3) i;
	analyze;
`

// wideColumns are more columns of 30 characters than fit the terminal.
var wideColumns = func() string {
	cols := make([]string, 12)
	for i := range cols {
		cols[i] = fmt.Sprintf("repeat('x', 30) as c%d", i+1)
	}
	return strings.Join(cols, ", ")
}()

// liveModel is a model connected to a database of its own, with the overview loaded.
func liveModel(t *testing.T) (model, *pgxpool.Pool) {
	t.Helper()
	pool, name := pgtest.Database(t, seed)
	cfg := pgtest.Config(t, name)
	m := newModel("test", "pgui")
	m.width, m.height = 160, 50
	m.pool, m.config = pool, cfg
	m.info = client.InfoOf(cfg, false)
	m.session = pgui.NewSession(cfg.ConnConfig)
	t.Cleanup(m.session.Close)
	m = drain(t, m, m.Init())
	return m, pool
}

func TestDatabasesLoaded(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m, cmd := update(t, m, messages.DatabasesLoadedMsg{Databases: []pgui.Database{{Name: "postgres", Owner: "postgres", SizeKnown: true, Size: 8 << 20}, {Name: "shop", Owner: "app"}}})
	if m.loading || len(m.state.databaseOverview.databasesTable.Rows()) != 2 {
		t.Fatalf("expected the databases listed, got loading %v rows %d", m.loading, len(m.state.databaseOverview.databasesTable.Rows()))
	}
	if cmd == nil {
		t.Error("expected a refresh to be scheduled")
	}
	if s := screen(m); !strings.Contains(s, "shop") || !strings.Contains(s, "8.0 MiB") {
		t.Errorf("expected the databases on screen:\n%s", s)
	}
}

func TestLoadErrorRaisesDialogOnce(t *testing.T) {
	m := newTestModel()
	m, _ = update(t, m, messages.DatabasesLoadedMsg{Err: errors.New("boom")})
	if !strings.Contains(m.error, "boom") {
		t.Fatalf("expected the error, got %q", m.error)
	}
	m, cmd := update(t, m, press("q"))
	if m.error != "" || cmd != nil {
		t.Error("expected any key to dismiss the error without quitting")
	}
	// With the databases on screen a failed refresh goes to the footer.
	m, _ = update(t, m, messages.DatabasesLoadedMsg{Databases: []pgui.Database{{Name: "a"}}}, messages.DatabasesLoadedMsg{Err: errors.New("again")})
	if m.error != "" || !strings.Contains(m.statusMsg, "again") {
		t.Errorf("expected the refresh error in the footer, got dialog %q status %q", m.error, m.statusMsg)
	}
}

func TestEscNeverQuits(t *testing.T) {
	m := newTestModel()
	m, _ = update(t, m, messages.DatabasesLoadedMsg{Databases: []pgui.Database{{Name: "a"}}})
	if _, cmd := update(t, m, press("esc")); cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("expected esc not to quit")
		}
	}
	if _, cmd := update(t, m, press("q")); cmd == nil {
		t.Error("expected q to quit on the overview")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("expected q to quit on the overview")
	}
}

func TestErrorTextOfServerErrors(t *testing.T) {
	ctx := context.Background()
	pool, _ := pgtest.Database(t)
	_, err := pool.Exec(ctx, "select * from missing")
	if got := errorText(err); got != `relation "missing" does not exist` {
		t.Errorf("expected the message without severity and code, got %q", got)
	}
}

func TestValueCell(t *testing.T) {
	tests := []struct {
		v    pgui.Value
		oid  uint32
		want string
	}{
		{pgui.Value{Null: true}, 25, "null"},
		{pgui.Value{}, 25, `""`},
		{pgui.Value{Text: "t"}, oidBool, "true"},
		{pgui.Value{Text: "a\nb\tc"}, 25, "a b c"},
		{pgui.Value{Text: "\x1b[31mred"}, 25, `\x1b[31mred`},
		{pgui.Value{Text: `{"a": 1}`}, oidJSONB, `{"a": 1}`},
	}
	for _, tt := range tests {
		var got string
		for _, s := range valueCell(tt.v, tt.oid) {
			got += s.Text
		}
		if got != tt.want {
			t.Errorf("valueCell(%+v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func TestRowJSON(t *testing.T) {
	fields := []pgui.Field{{Name: "id", Type: 20}, {Name: "meta", Type: oidJSONB}, {Name: "note", Type: 25}}
	row := pgui.Row{Values: []pgui.Value{{Text: "1"}, {Text: `{"a": [1]}`}, {Null: true}}}
	if got := rowJSON(fields, row); got != `{"id": "1", "meta": {"a": [1]}, "note": null}` {
		t.Errorf("unexpected %s", got)
	}
}

func hintKeys(m model) []string {
	var keys []string
	for _, p := range m.shortHelp() {
		keys = append(keys, p[0]+" "+p[1])
	}
	return keys
}

// Every page offers the same keys in the same order, and leaves out the ones it would refuse.
func TestShortHelpIsConsistent(t *testing.T) {
	m, _ := update(t, newTestModel(), messages.DatabasesLoadedMsg{Databases: []pgui.Database{{Name: "shop"}}})
	keys := hintKeys(m)
	if keys[0] != "enter open" || keys[2] != "tab activity" || keys[3] != "/ filter" || keys[len(keys)-2] != "? help" || keys[len(keys)-1] != "q quit" {
		t.Errorf("unexpected order %v", keys)
	}
	m.info.ReadOnly = true
	for _, k := range hintKeys(m) {
		if strings.HasPrefix(k, "ctrl+n") || strings.HasPrefix(k, "ctrl+d") {
			t.Errorf("expected %q left out on a read only connection", k)
		}
	}

	view := pgui.Table{OID: 1, Schema: "s", Name: "v", Kind: pgui.KindView}
	m, _ = newTestModel().TableDetailsSwitchPage(view)
	m, _ = update(t, m, messages.SchemaLoadedMsg{OID: 1})
	for _, k := range hintKeys(m) {
		if strings.HasPrefix(k, "ctrl+d") || strings.HasPrefix(k, "space") || strings.HasPrefix(k, "f ") {
			t.Errorf("expected %q left out for the rows of a view", k)
		}
	}
	m, _ = update(t, m, press("tab"))
	keys = hintKeys(m)
	if !slices.Contains(keys, "tab indexes") || slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, "ctrl+p") }) {
		t.Errorf("expected the next tab named and no truncate for a view, got %v", keys)
	}
}
