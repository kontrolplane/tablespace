package tablespace

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Paging says how the rows of a table are paged through, newest first: by the primary key, by
// ctid for a table without one, or by offset for what has neither, like a view.
type Paging struct {
	Key   []string // the primary key columns
	Types []string // the types of the key columns
	CTID  bool
}

// ByOffset reports whether the rows are paged by offset, which has no order to keep.
func (p Paging) ByOffset() bool { return len(p.Key) == 0 && !p.CTID }

// Deletable reports whether a row can be found again to delete it.
func (p Paging) Deletable() bool { return len(p.Key) > 0 }

// Describe words the order the rows are shown in.
func (p Paging) Describe() string {
	switch {
	case len(p.Key) > 0:
		return "newest first by " + strings.Join(p.Key, ", ")
	case p.CTID:
		return "newest first by ctid, no primary key"
	}
	return "in no particular order"
}

// GetPaging works out how to page through the rows of t.
func GetPaging(ctx context.Context, pool *pgxpool.Pool, t Table) (Paging, error) {
	var p Paging
	rows, err := pool.Query(ctx, `
		select a.attname, format_type(a.atttypid, a.atttypmod)
		from pg_index x
		cross join lateral unnest(x.indkey) with ordinality as k(attnum, n)
		join pg_attribute a on a.attrelid = x.indrelid and a.attnum = k.attnum
		where x.indrelid = $1 and x.indisprimary
		order by k.n`, t.OID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			rows.Close()
			return p, err
		}
		p.Key = append(p.Key, name)
		p.Types = append(p.Types, typ)
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	// The ctid of a row is only unique within the table holding it, which a partitioned table
	// does not.
	if len(p.Key) == 0 && (t.Kind == KindTable || t.Kind == KindMaterialized) {
		p.CTID = true
	}
	return p, nil
}

// Page is a page of rows of a table.
type Page struct {
	Fields []Field
	Rows   []Row
	// Next is where the next older page starts, the key of the last row or the offset past it,
	// nil when there is none.
	Next []string
}

// RowsQuery asks for a page of rows.
type RowsQuery struct {
	Table  Table
	Paging Paging
	Where  string   // a condition the rows meet, as sql
	After  []string // where the page starts, from Page.Next, nil for the newest rows
	Limit  int
}

// keyExprs are the select expressions of the key of a row.
func (q RowsQuery) keyExprs() []string {
	if q.Paging.CTID {
		return []string{"t.ctid"}
	}
	exprs := make([]string, len(q.Paging.Key))
	for i, k := range q.Paging.Key {
		exprs[i] = "t." + QuoteIdent(k)
	}
	return exprs
}

// keyTypes are the types to read the key of a row back as.
func (q RowsQuery) keyTypes() []string {
	if q.Paging.CTID {
		return []string{"tid"}
	}
	return q.Paging.Types
}

// sql builds the query for the page, with its arguments. The values of the key come back as text
// and go out as text, cast to the type of their column.
func (q RowsQuery) sql() (string, []any) {
	keys := q.keyExprs()
	var b strings.Builder
	b.WriteString("select ")
	for _, k := range keys {
		b.WriteString(k + ", ")
	}
	b.WriteString("t.* from " + q.Table.QualifiedName() + " t")

	var conds []string
	var args []any
	if w := strings.TrimSpace(q.Where); w != "" {
		// On lines of their own, so a comment at the end of the condition ends there.
		conds = append(conds, "(\n"+w+"\n)")
	}
	if len(keys) > 0 && q.After != nil {
		params := make([]string, len(keys))
		types := q.keyTypes()
		for i := range keys {
			args = append(args, q.After[i])
			params[i] = fmt.Sprintf("$%d::text::%s", len(args), types[i])
		}
		conds = append(conds, "("+strings.Join(keys, ", ")+") < ("+strings.Join(params, ", ")+")")
	}
	if len(conds) > 0 {
		b.WriteString(" where " + strings.Join(conds, " and "))
	}
	if len(keys) > 0 {
		desc := make([]string, len(keys))
		for i, k := range keys {
			desc[i] = k + " desc"
		}
		b.WriteString(" order by " + strings.Join(desc, ", "))
	}
	fmt.Fprintf(&b, " limit %d", q.Limit+1)
	if q.Paging.ByOffset() && q.After != nil {
		b.WriteString(" offset " + q.After[0])
	}
	return b.String(), args
}

// ListRows loads a page of rows, newest first where the table has an order.
func ListRows(ctx context.Context, pool *pgxpool.Pool, q RowsQuery) (Page, error) {
	if q.Paging.ByOffset() && q.After != nil {
		if _, err := strconv.Atoi(q.After[0]); err != nil {
			return Page{}, errors.New("invalid offset")
		}
	}
	sql, args := q.sql()
	args = append([]any{pgx.QueryResultFormats{pgx.TextFormatCode}}, args...)
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()

	keys := len(q.keyExprs())
	var page Page
	for _, f := range rows.FieldDescriptions()[keys:] {
		page.Fields = append(page.Fields, Field{Name: f.Name, Type: f.DataTypeOID})
	}
	for rows.Next() {
		raw := rows.RawValues()
		row := Row{Key: make([]string, keys), Values: make([]Value, len(raw)-keys)}
		for i := range keys {
			row.Key[i] = string(raw[i])
		}
		for i, v := range raw[keys:] {
			row.Values[i] = Value{Text: string(v), Null: v == nil}
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Rows) > q.Limit {
		page.Rows = page.Rows[:q.Limit]
		if q.Paging.ByOffset() {
			offset := 0
			if q.After != nil {
				offset, _ = strconv.Atoi(q.After[0])
			}
			page.Next = []string{strconv.Itoa(offset + q.Limit)}
		} else {
			page.Next = page.Rows[len(page.Rows)-1].Key
		}
	}
	return page, nil
}

// DeleteRows deletes the rows of t with the given primary keys, in one transaction, and returns
// how many it deleted.
func DeleteRows(ctx context.Context, pool *pgxpool.Pool, t Table, p Paging, keys [][]string) (int64, error) {
	if !p.Deletable() {
		return 0, errors.New("the table has no primary key to find the rows by")
	}
	cols := make([]string, len(p.Key))
	params := make([]string, len(p.Key))
	for i, k := range p.Key {
		cols[i] = QuoteIdent(k)
		params[i] = fmt.Sprintf("$%d::text::%s", i+1, p.Types[i])
	}
	sql := "delete from " + t.QualifiedName() + " where (" + strings.Join(cols, ", ") + ") = (" + strings.Join(params, ", ") + ")"

	var deleted int64
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		for _, key := range keys {
			args := make([]any, len(key))
			for i, k := range key {
				args[i] = k
			}
			tag, err := tx.Exec(ctx, sql, args...)
			if err != nil {
				return err
			}
			deleted += tag.RowsAffected()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
