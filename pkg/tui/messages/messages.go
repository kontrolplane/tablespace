// Package messages holds the messages the commands answer with. Each carries the error of the
// request it answers, and what it was about, so an answer that arrives after the page moved on
// can be told apart and dropped.
package messages

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kontrolplane/pgui/pkg/pgui"
)

type ServerLoadedMsg struct {
	Server pgui.Server
	Err    error
}

type ServerTickMsg struct{}

type DatabasesLoadedMsg struct {
	Databases []pgui.Database
	Err       error
}

type BackendsLoadedMsg struct {
	Backends []pgui.Backend
	Err      error
}

// DatabaseOpenedMsg answers connecting to another database, with the pool connected to it.
type DatabaseOpenedMsg struct {
	Name   string
	Pool   *pgxpool.Pool
	Config *pgxpool.Config
	Err    error
}

type TablesLoadedMsg struct {
	Database string
	Tables   []pgui.Table
	Err      error
}

type IndexesLoadedMsg struct {
	Database string
	Indexes  []pgui.Index
	Err      error
}

type TableLoadedMsg struct {
	OID   uint32
	Table pgui.Table
	Err   error
}

// SchemaLoadedMsg answers loading the columns, indexes and constraints of a table, and how its
// rows are paged.
type SchemaLoadedMsg struct {
	OID         uint32
	Columns     []pgui.Column
	Indexes     []pgui.Index
	Constraints []pgui.Constraint
	Paging      pgui.Paging
	Err         error
}

// RowsLoadedMsg answers loading a page of rows. After is where the page starts, nil for the
// newest rows; Gen numbers the load, so only the latest one is shown.
type RowsLoadedMsg struct {
	OID   uint32
	Gen   uint64
	After []string
	Page  pgui.Page
	Err   error
}

type RowsDeletedMsg struct {
	OID     uint32
	Deleted int64
	Err     error
}

type DatabaseCreatedMsg struct {
	Name string
	Err  error
}

type DatabasesDroppedMsg struct {
	Names []string
	Err   error
}

// BackendsSignalledMsg answers cancelling the queries of backends, or terminating them.
type BackendsSignalledMsg struct {
	Terminate bool
	PIDs      []int
	Err       error
}

type TableTruncatedMsg struct {
	Table pgui.Table
	Err   error
}

type TableDroppedMsg struct {
	Table pgui.Table
	Err   error
}

// QueryRanMsg answers running the editor's query.
type QueryRanMsg struct {
	Gen    uint64
	Result pgui.QueryResult
	Err    error
}

type ClipboardCopiedMsg struct {
	Text string
	Err  error
}

type StatusClearMsg struct {
	Gen int
}

type RefreshTickMsg struct {
	Gen int
}

type ClockTickMsg struct{}
