// Package commands provides tea.Cmd factories for async operations.
package commands

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/messages"
)

// RefreshInterval is the interval for auto-refresh of the visible data.
const RefreshInterval = 5 * time.Second

// RowLimit is the number of rows loaded at a time.
const RowLimit = 100

const requestTimeout = 10 * time.Second

// request runs fn in a tea.Cmd with the request timeout applied to ctx.
func request(ctx context.Context, fn func(context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		return fn(ctx)
	}
}

func LoadServer(ctx context.Context, pool *pgxpool.Pool) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		server, err := pgui.GetServer(ctx, pool)
		return messages.ServerLoadedMsg{Server: server, Err: err}
	})
}

func ScheduleServerRefresh() tea.Cmd {
	return tea.Tick(RefreshInterval, func(time.Time) tea.Msg {
		return messages.ServerTickMsg{}
	})
}

func LoadDatabases(ctx context.Context, pool *pgxpool.Pool) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		dbs, err := pgui.ListDatabases(ctx, pool)
		return messages.DatabasesLoadedMsg{Databases: dbs, Err: err}
	})
}

func LoadBackends(ctx context.Context, pool *pgxpool.Pool) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		backends, err := pgui.ListBackends(ctx, pool)
		return messages.BackendsLoadedMsg{Backends: backends, Err: err}
	})
}

// OpenDatabase connects a new pool to the database cfg names.
func OpenDatabase(ctx context.Context, cfg *pgxpool.Config) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		name := cfg.ConnConfig.Database
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err != nil {
				pool.Close()
				pool = nil
			}
		}
		return messages.DatabaseOpenedMsg{Name: name, Pool: pool, Config: cfg, Err: err}
	})
}

// ClosePool closes a pool that was replaced, off the update loop.
func ClosePool(pool *pgxpool.Pool) tea.Cmd {
	return func() tea.Msg {
		pool.Close()
		return nil
	}
}

func LoadTables(ctx context.Context, pool *pgxpool.Pool, database string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		tables, err := pgui.ListTables(ctx, pool)
		return messages.TablesLoadedMsg{Database: database, Tables: tables, Err: err}
	})
}

func LoadIndexes(ctx context.Context, pool *pgxpool.Pool, database string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		indexes, err := pgui.ListIndexes(ctx, pool)
		return messages.IndexesLoadedMsg{Database: database, Indexes: indexes, Err: err}
	})
}

func LoadTable(ctx context.Context, pool *pgxpool.Pool, oid uint32) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		t, err := pgui.GetTable(ctx, pool, oid)
		return messages.TableLoadedMsg{OID: oid, Table: t, Err: err}
	})
}

// LoadSchema loads the columns, indexes and constraints of t, and how its rows are paged.
func LoadSchema(ctx context.Context, pool *pgxpool.Pool, t pgui.Table) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		msg := messages.SchemaLoadedMsg{OID: t.OID}
		if msg.Columns, msg.Err = pgui.ListColumns(ctx, pool, t.OID); msg.Err != nil {
			return msg
		}
		if msg.Indexes, msg.Err = pgui.ListTableIndexes(ctx, pool, t.OID); msg.Err != nil {
			return msg
		}
		if msg.Constraints, msg.Err = pgui.ListConstraints(ctx, pool, t.OID); msg.Err != nil {
			return msg
		}
		msg.Paging, msg.Err = pgui.GetPaging(ctx, pool, t)
		return msg
	})
}

// LoadRows loads the page of rows of t that starts at after, the newest when it is nil.
func LoadRows(ctx context.Context, pool *pgxpool.Pool, t pgui.Table, paging pgui.Paging, where string, after []string, gen uint64) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		page, err := pgui.ListRows(ctx, pool, pgui.RowsQuery{Table: t, Paging: paging, Where: where, After: after, Limit: RowLimit})
		return messages.RowsLoadedMsg{OID: t.OID, Gen: gen, After: after, Page: page, Err: err}
	})
}

func DeleteRows(ctx context.Context, pool *pgxpool.Pool, t pgui.Table, paging pgui.Paging, keys [][]string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		n, err := pgui.DeleteRows(ctx, pool, t, paging, keys)
		return messages.RowsDeletedMsg{OID: t.OID, Deleted: n, Err: err}
	})
}

func CreateDatabase(ctx context.Context, pool *pgxpool.Pool, cfg pgui.DatabaseConfig) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		err := pgui.CreateDatabase(ctx, pool, cfg)
		return messages.DatabaseCreatedMsg{Name: cfg.Name, Err: err}
	})
}

func DropDatabases(ctx context.Context, pool *pgxpool.Pool, names []string) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		dropped, err := pgui.DropDatabases(ctx, pool, names)
		return messages.DatabasesDroppedMsg{Names: dropped, Err: err}
	})
}

// SignalBackends cancels the queries of the backends, or terminates them.
func SignalBackends(ctx context.Context, pool *pgxpool.Pool, pids []int, terminate bool) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		signal := pgui.CancelBackends
		if terminate {
			signal = pgui.TerminateBackends
		}
		done, err := signal(ctx, pool, pids)
		return messages.BackendsSignalledMsg{Terminate: terminate, PIDs: done, Err: err}
	})
}

func TruncateTable(ctx context.Context, pool *pgxpool.Pool, t pgui.Table) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		return messages.TableTruncatedMsg{Table: t, Err: pgui.TruncateTable(ctx, pool, t)}
	})
}

func DropTable(ctx context.Context, pool *pgxpool.Pool, t pgui.Table) tea.Cmd {
	return request(ctx, func(ctx context.Context) tea.Msg {
		return messages.TableDroppedMsg{Table: t, Err: pgui.DropTable(ctx, pool, t)}
	})
}

// RunQuery runs the editor's sql on its session. It takes as long as it takes, cancelling ctx
// cancels it.
func RunQuery(ctx context.Context, session *pgui.Session, sql string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		res, err := session.Run(ctx, sql)
		return messages.QueryRanMsg{Gen: gen, Result: res, Err: err}
	}
}

// CloseSession closes an editor session that was replaced, off the update loop.
func CloseSession(session *pgui.Session) tea.Cmd {
	return func() tea.Msg {
		session.Close()
		return nil
	}
}

func ScheduleRefresh(gen int) tea.Cmd {
	return tea.Tick(RefreshInterval, func(time.Time) tea.Msg {
		return messages.RefreshTickMsg{Gen: gen}
	})
}

// CopyToClipboard copies text using the system clipboard. On failure the update
// loop falls back to OSC52, which also works over ssh.
func CopyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		return messages.ClipboardCopiedMsg{Text: text, Err: clipboard.WriteAll(text)}
	}
}

// ScheduleClock ticks once after d, for a clock on screen that changes then.
func ScheduleClock(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return messages.ClockTickMsg{}
	})
}

func ClearStatusAfter(d time.Duration, gen int) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return messages.StatusClearMsg{Gen: gen}
	})
}
