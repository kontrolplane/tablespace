// Package tablespace reads and changes what the interface shows: databases, tables and their
// rows, sessions, and the queries typed in the editor. It returns its own types, so the interface
// never handles a pgx value.
package tablespace

import (
	"strings"
	"time"
)

// Server describes the server and the database the pool is connected to.
type Server struct {
	Version        string
	Database       string
	User           string
	SSL            bool
	Replica        bool
	ReadOnly       bool // transactions start read only
	Connections    int
	MaxConnections int
	DatabaseSize   uint64
	Started        time.Time
	RTT            time.Duration
}

// Database is a database of the cluster.
type Database struct {
	Name        string
	Owner       string
	Encoding    string
	Collation   string
	Size        uint64
	SizeKnown   bool // the size is only known for databases the user may connect to
	Connections int
	AllowConn   bool
	Template    bool
	Comment     string
}

// Table is a relation that holds or returns rows: a table, view, materialized view, partitioned
// or foreign table.
type Table struct {
	OID         uint32
	Schema      string
	Name        string
	Kind        string
	Partition   bool
	Owner       string
	Comment     string
	Estimate    int64 // the planner's row estimate, -1 when the table was never analyzed
	LiveRows    int64
	DeadRows    int64
	TotalBytes  uint64
	TableBytes  uint64
	IndexBytes  uint64
	SeqScans    int64
	IdxScans    int64
	Inserted    int64
	Updated     int64
	Deleted     int64
	LastVacuum  time.Time
	LastAnalyze time.Time
}

// QualifiedName is the table as it is written in a query, quoted where it needs to be.
func (t Table) QualifiedName() string {
	return QuoteIdent(t.Schema) + "." + QuoteIdent(t.Name)
}

// DisplayName is schema.name, unquoted.
func (t Table) DisplayName() string {
	return t.Schema + "." + t.Name
}

// HoldsRows reports whether the table stores its own rows, so it can be truncated and its
// rows deleted.
func (t Table) HoldsRows() bool {
	switch t.Kind {
	case KindTable, KindPartitioned, KindForeign:
		return true
	}
	return false
}

// The kinds of tables.
const (
	KindTable        = "table"
	KindPartitioned  = "partitioned"
	KindView         = "view"
	KindMaterialized = "matview"
	KindForeign      = "foreign"
)

// Column is a column of a table.
type Column struct {
	Name      string
	Type      string
	Nullable  bool
	Default   string
	Identity  string // "always" or "by default"
	Generated bool
	Comment   string
}

// Index is an index of a table.
type Index struct {
	Schema     string
	Table      string
	Name       string
	Definition string
	Bytes      uint64
	Scans      int64
	Unique     bool
	Primary    bool
	Valid      bool
}

// Method is what the definition of an index says past its name and table, like btree (email) or
// gin (meta) where (meta is not null).
func (i Index) Method() string {
	if _, after, ok := strings.Cut(i.Definition, " USING "); ok {
		return after
	}
	return i.Definition
}

// Constraint is a constraint of a table.
type Constraint struct {
	Name       string
	Kind       string // primary key, foreign key, unique, check, exclude
	Definition string
	References string // the table a foreign key points at
}

// Value is a value as postgres writes it out, the way psql shows it.
type Value struct {
	Text string
	Null bool
}

// Field is a column of a result.
type Field struct {
	Name string
	Type uint32 // the oid of its type
}

// Row is a row of a table, with the key it is found by again.
type Row struct {
	Key    []string
	Values []Value
}

// Backend is a session or background process of the server.
type Backend struct {
	PID           int
	User          string
	Database      string
	Application   string
	Client        string
	State         string
	WaitEventType string
	WaitEvent     string
	Query         string
	Type          string
	BackendStart  time.Time
	XactStart     time.Time
	QueryStart    time.Time
	StateChange   time.Time
	BlockedBy     []int
	Self          bool // the session the program itself runs on
}

// Blocks returns the pids of the backends waiting on the one with pid.
func Blocks(pid int, backends []Backend) []int {
	var out []int
	for _, o := range backends {
		for _, p := range o.BlockedBy {
			if p == pid {
				out = append(out, o.PID)
				break
			}
		}
	}
	return out
}
