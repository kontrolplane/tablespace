// Package messages holds the messages the commands answer with. Each carries the error of the
// request it answers, and what it was about, so an answer that arrives after the page moved on
// can be told apart and dropped.
package messages

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kontrolplane/tablespace/pkg/tablespace"
)

type ServerLoadedMsg struct {
	Server tablespace.Server
	Err    error
}

type ServerTickMsg struct{}

type DatabasesLoadedMsg struct {
	Databases []tablespace.Database
	Err       error
}

type BackendsLoadedMsg struct {
	Backends []tablespace.Backend
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
	Tables   []tablespace.Table
	Err      error
}

type IndexesLoadedMsg struct {
	Database string
	Indexes  []tablespace.Index
	Err      error
}

type TableLoadedMsg struct {
	OID   uint32
	Table tablespace.Table
	Err   error
}

// SchemaLoadedMsg answers loading the columns, indexes and constraints of a table, and how its
// rows are paged.
type SchemaLoadedMsg struct {
	OID         uint32
	Columns     []tablespace.Column
	Indexes     []tablespace.Index
	Constraints []tablespace.Constraint
	Paging      tablespace.Paging
	Err         error
}

// RowsLoadedMsg answers loading a page of rows. After is where the page starts, nil for the
// newest rows; Gen numbers the load, so only the latest one is shown.
type RowsLoadedMsg struct {
	OID   uint32
	Gen   uint64
	After []string
	Page  tablespace.Page
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
	Table tablespace.Table
	Err   error
}

type TableDroppedMsg struct {
	Table tablespace.Table
	Err   error
}

// QueryRanMsg answers running the editor's query.
type QueryRanMsg struct {
	Gen    uint64
	Result tablespace.QueryResult
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
