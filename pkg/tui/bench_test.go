package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tablespace/pkg/tablespace"
	"github.com/kontrolplane/tablespace/pkg/tui/messages"
)

// BenchmarkRenderRows draws a frame of the rows tab, full of JSON values. The view is drawn after
// every message the program gets, keys and refreshes alike.
func BenchmarkRenderRows(b *testing.B) {
	defer setLayout(140, 25)
	t := &testing.T{}
	m, _ := update(t, newTestModel(), tea.WindowSizeMsg{Width: 200, Height: 50})
	table := tablespace.Table{OID: 1, Schema: "shop", Name: "orders", Kind: tablespace.KindTable}
	m, _ = m.TableDetailsSwitchPage(table)
	fields := []tablespace.Field{{Name: "id", Type: 20}, {Name: "customer", Type: 25}, {Name: "total", Type: 1700}, {Name: "meta", Type: oidJSONB}, {Name: "note", Type: 25}}
	rows := make([]tablespace.Row, 100)
	for i := range rows {
		rows[i] = tablespace.Row{Key: []string{fmt.Sprint(100 - i)}, Values: []tablespace.Value{
			{Text: fmt.Sprint(100 - i)}, {Text: fmt.Sprintf("cus_%04d", i)}, {Text: fmt.Sprintf("%d.95", i)},
			{Text: fmt.Sprintf(`{"status": "created", "items": [%d, %d], "msg": "order placed"}`, i, i+1)}, {Null: true}}}
	}
	m, _ = update(t, m,
		messages.TableLoadedMsg{OID: 1, Table: table},
		messages.SchemaLoadedMsg{OID: 1, Paging: tablespace.Paging{Key: []string{"id"}, Types: []string{"bigint"}}},
	)
	m, _ = update(t, m, messages.RowsLoadedMsg{OID: 1, Gen: m.rowsGen, Page: tablespace.Page{Fields: fields, Rows: rows, Next: []string{"1"}}})
	if m.page != tableDetails || len(m.state.tableDetails.rows) == 0 {
		b.Fatalf("page %v with %d rows", m.page, len(m.state.tableDetails.rows))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.render()
	}
}
