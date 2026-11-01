package pgui

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MaxResultRows bounds the rows of a result the editor keeps. The rest are read and counted.
const MaxResultRows = 1000

// Statement is the outcome of one statement of what the editor ran.
type Statement struct {
	Tag string // e.g. "INSERT 0 3" or "SELECT 12"
}

// QueryResult is what the editor ran: the tag of every statement, and the rows of the last one
// that returned rows.
type QueryResult struct {
	Statements []Statement
	Fields     []Field
	Rows       [][]Value
	Total      int64 // rows the statement returned, including those past MaxResultRows
	Duration   time.Duration
	InTx       bool // a transaction is still open after it
}

// Truncated reports whether rows were left out of the result.
func (r QueryResult) Truncated() bool { return r.Total > int64(len(r.Rows)) }

// Session is the connection the editor runs on, opened on its first query. It stays open, so a
// transaction begun or a setting changed in one run is still there in the next.
type Session struct {
	cfg *pgx.ConnConfig

	mu   sync.Mutex
	conn *pgx.Conn
}

// NewSession returns a session that will connect with cfg.
func NewSession(cfg *pgx.ConnConfig) *Session {
	return &Session{cfg: cfg}
}

// Close closes the connection of the session, if it was opened.
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.conn.Close(ctx)
		s.conn = nil
	}
}

func (s *Session) connect(ctx context.Context) (*pgx.Conn, error) {
	if s.conn != nil && !s.conn.IsClosed() {
		return s.conn, nil
	}
	conn, err := pgx.ConnectConfig(ctx, s.cfg)
	if err != nil {
		return nil, err
	}
	s.conn = conn
	return conn, nil
}

// Run runs sql, which may hold several statements, as the simple protocol does: in one
// transaction unless it manages its own.
func (s *Session) Run(ctx context.Context, sql string) (QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res QueryResult
	conn, err := s.connect(ctx)
	if err != nil {
		return res, err
	}
	start := time.Now()
	mrr := conn.PgConn().Exec(ctx, sql)
	for mrr.NextResult() {
		rr := mrr.ResultReader()
		fields := rr.FieldDescriptions()
		var rows [][]Value
		var total int64
		for rr.NextRow() {
			total++
			if len(rows) >= MaxResultRows {
				continue
			}
			raw := rr.Values()
			row := make([]Value, len(raw))
			for i, v := range raw {
				row[i] = Value{Text: string(v), Null: v == nil}
			}
			rows = append(rows, row)
		}
		tag, err := rr.Close()
		if err != nil {
			break
		}
		res.Statements = append(res.Statements, Statement{Tag: tag.String()})
		if fields != nil {
			res.Fields = make([]Field, len(fields))
			for i, f := range fields {
				res.Fields[i] = Field{Name: f.Name, Type: f.DataTypeOID}
			}
			res.Rows, res.Total = rows, total
		}
	}
	err = mrr.Close()
	res.Duration = time.Since(start)
	res.InTx = conn.PgConn().TxStatus() != 'I'
	if conn.IsClosed() {
		s.conn = nil
	}
	return res, err
}

// IsCanceled reports whether err is a query that was cancelled, by the editor or elsewhere.
func IsCanceled(err error) bool {
	var pgErr *pgconn.PgError
	return errors.Is(err, context.Canceled) || (errors.As(err, &pgErr) && pgErr.Code == "57014")
}
