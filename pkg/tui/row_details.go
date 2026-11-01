package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/commands"
	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

// rowDetailsState shows one row: its fields on the left, the value of the one under the cursor
// in full on the right. It opens from the rows of a table or from a result of the editor.
type rowDetailsState struct {
	fields    []pgui.Field
	types     []string // the type of each field, where it is known
	row       pgui.Row
	key       []string // the primary key of the row, for a row of a table
	fromQuery bool
	index     int // the row's place in the result it came from
	table     dataTable
	value     scroller
}

var rowFieldColumns = []column{
	{title: "column", width: 18, grow: 1, min: 10},
	{title: "type", width: 16, drop: 1},
	{title: "value", width: 30, grow: 2},
}

// title names the row in the breadcrumb by its key.
func (d rowDetailsState) title() string {
	if d.fromQuery {
		return fmt.Sprintf("row %d", d.index+1)
	}
	if len(d.key) == 0 {
		return "row"
	}
	return strings.Join(d.key, ", ")
}

// RowDetailsSwitchPage opens a row of the open table.
func (m model) RowDetailsSwitchPage(r pgui.Row) (model, tea.Cmd) {
	td := m.state.tableDetails
	types := make([]string, len(td.fields))
	for i, f := range td.fields {
		if j := slices.IndexFunc(td.columns, func(c pgui.Column) bool { return c.Name == f.Name }); j >= 0 {
			types[i] = td.columns[j].Type
		}
	}
	var keyOf []string
	if td.paging.Deletable() {
		keyOf = r.Key
	}
	return m.openRow(rowDetailsState{fields: td.fields, types: types, row: r, key: keyOf}), nil
}

// openRow shows the row d holds.
func (m model) openRow(d rowDetailsState) model {
	m.error = ""
	d.table = newDataTable(rowFieldColumns, leftContentWidth, contentHeight-tableHeaderRows)
	d.value = newScroller()
	m.state.rowDetails = d
	m = m.SwitchPage(rowDetails).updateRowFieldsTable()
	return m.resizeRowDetails()
}

func (m model) resizeRowDetails() model {
	d := &m.state.rowDetails
	if d.fields == nil {
		return m
	}
	d.table.setSize(leftContentWidth, contentHeight-tableHeaderRows)
	return m
}

func (m model) updateRowFieldsTable() model {
	d := &m.state.rowDetails
	rows := make([]tableRow, len(d.fields))
	for i, f := range d.fields {
		var v pgui.Value
		if i < len(d.row.Values) {
			v = d.row.Values[i]
		}
		name := text(f.Name, styles.ToneText)
		if slices.Contains(d.keyColumns(m), f.Name) {
			name = text(f.Name, styles.ToneAccent)
		}
		rows[i] = tableRow{name, text(orDash(d.types[i]), styles.ToneFaint), valueCell(v, f.Type)}
	}
	setRows(&d.table, rows, d.table.Cursor())
	return m
}

// keyColumns are the primary key columns of the row, when it is a row of a table.
func (d rowDetailsState) keyColumns(m model) []string {
	if d.fromQuery {
		return nil
	}
	return m.state.tableDetails.paging.Key
}

// currentValue is the field under the cursor.
func (d rowDetailsState) currentValue() (pgui.Field, pgui.Value, bool) {
	i := d.table.Cursor()
	if i >= len(d.fields) || i >= len(d.row.Values) {
		return pgui.Field{}, pgui.Value{}, false
	}
	return d.fields[i], d.row.Values[i], true
}

// renderValue renders the value under the cursor in full: JSON pretty printed, null and the
// empty string named.
func (d rowDetailsState) renderValue() string {
	f, v, ok := d.currentValue()
	switch {
	case !ok:
		return ""
	case v.Null:
		return styles.Faint("null")
	}
	return newPayloadText([]byte(v.Text), isJSONType(f.Type)).render(rightContentWidth)
}

func (m model) rowDetailsGoBack() (model, tea.Cmd) {
	if m.state.rowDetails.fromQuery {
		m.error = ""
		return m.SwitchPage(sqlEditor), nil
	}
	return m.TableDetailsGoBack()
}

func (m model) RowDetailsView() string {
	d := m.state.rowDetails
	f, v, ok := d.currentValue()
	meta := ""
	if ok {
		kind := "text"
		switch {
		case v.Null:
			kind = "null"
		case d.types[d.table.Cursor()] != "":
			kind = d.types[d.table.Cursor()]
		}
		// The size is only worth saying for a value longer than a line.
		meta = styles.Faint(kind)
		if len(v.Text) > rightContentWidth {
			meta = styles.Faint(fmt.Sprintf("%s · %s", kind, formatBytes(uint64(len(v.Text)))))
		}
	}
	header := styles.SectionHeaderWith(styles.Fg(styles.ToneAccent).Bold(true).Render(truncate(styles.Clean(f.Name), rightContentWidth/2)), meta, rightContentWidth)
	view := d.value
	view.setContent(d.renderValue())
	return splitPanels(d.table.View(), header+"\n"+view.view(contentHeight-1))
}

func (m model) RowDetailsUpdate(msg tea.Msg) (model, tea.Cmd) {
	d := &m.state.rowDetails
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(keyMsg, m.keys.Back), key.Matches(keyMsg, m.keys.Quit):
		return m.rowDetailsGoBack()
	case key.Matches(keyMsg, m.keys.CopyToClipboard):
		if _, v, ok := d.currentValue(); ok && !v.Null {
			return m, commands.CopyToClipboard(v.Text)
		}
	case key.Matches(keyMsg, m.keys.Delete):
		if d.fromQuery {
			return m.setStatus("a row of a result is deleted with a query", styles.ToneWarning)
		}
		return m.rowDeleteOpen(true)
	case keyMsg.String() == "pgup" || keyMsg.String() == "pgdown" || keyMsg.String() == "space":
		d.value.setContent(d.renderValue())
		d.value.update(keyMsg, contentHeight-1)
	default:
		prev := d.table.Cursor()
		d.table = d.table.Update(keyMsg)
		if d.table.Cursor() != prev {
			d.value = newScroller()
		}
	}
	return m, nil
}

// rowDeleteState asks to delete rows of the open table, by their primary key.
type rowDeleteState struct {
	keys        [][]string
	fromDetails bool
	selected    int
}

// rowDeleteOpen asks to delete the selected rows, the row under the cursor, or with fromDetails
// the open row.
func (m model) rowDeleteOpen(fromDetails bool) (model, tea.Cmd) {
	if m, cmd, refused := m.refuseReadOnly(); refused {
		return m, cmd
	}
	td := m.state.tableDetails
	switch {
	case !td.table.HoldsRows():
		return m.setStatus("rows of a "+td.table.Kind+" are deleted from the tables it reads", styles.ToneWarning)
	case !td.paging.Deletable():
		return m.setStatus(td.table.DisplayName()+" has no primary key to find the rows by, delete them with a query", styles.ToneWarning)
	}
	var keys [][]string
	if fromDetails {
		keys = [][]string{m.state.rowDetails.row.Key}
	} else {
		var hidden int
		var picked []string
		picked, hidden = selectedWithHidden(td.selectedRows, m.getFilteredRows(), rowKey, m.currentRow)
		if len(picked) == 0 {
			if hidden > 0 {
				return m.setStatus("the selected rows are hidden by the search", styles.ToneWarning)
			}
			return m, nil
		}
		for _, k := range picked {
			keys = append(keys, strings.Split(k, "\x00"))
		}
	}
	m.error = ""
	m.state.rowDelete = rowDeleteState{keys: keys, fromDetails: fromDetails}
	return m.SwitchPage(rowDelete), nil
}

func (m model) RowDeleteView() string {
	s := m.state.rowDelete
	td := m.state.tableDetails
	target := styles.B(plural(len(s.keys), "row"), styles.ToneText)
	labels := make([]string, len(s.keys))
	for i, k := range s.keys {
		parts := make([]string, len(k))
		for j := range k {
			parts[j] = td.paging.Key[j] + " = " + k[j]
		}
		labels[i] = strings.Join(parts, ", ")
	}
	title := "delete rows"
	if len(s.keys) == 1 {
		target, title = styles.B(labels[0], styles.ToneText), "delete row"
		prompt := []styles.Span{styles.S("delete the row with ", styles.ToneBody), target, styles.S(" from ", styles.ToneBody), styles.B(dialogName(td.table.DisplayName()), styles.ToneText), styles.S("?", styles.ToneBody)}
		return confirmDialog(title, prompt, []string{"", styles.Faint("this cannot be undone.")}, s.selected)
	}
	prompt := []styles.Span{styles.S("delete ", styles.ToneBody), target, styles.S(" from ", styles.ToneBody), styles.B(dialogName(td.table.DisplayName()), styles.ToneText), styles.S("?", styles.ToneBody)}
	body := listBody(labels, false)
	body = append(body, "", styles.Faint("this cannot be undone. the rows are deleted in one transaction."))
	return confirmDialog(title, prompt, body, s.selected)
}

func (m model) RowDeleteUpdate(msg tea.Msg) (model, tea.Cmd) {
	s := &m.state.rowDelete
	switch m.confirmKey(msg, &s.selected) {
	case confirmYes:
		if m, refused := m.refuseOffline("deleted"); refused {
			return m, nil
		}
		td := m.state.tableDetails
		m.busy, m.loading, m.loadingMsg = true, true, "deleting "+plural(len(s.keys), "row")+"…"
		return m, commands.DeleteRows(m.context, m.pool, td.table, td.paging, s.keys)
	case confirmNo:
		if s.fromDetails {
			m.error = ""
			return m.SwitchPage(rowDetails), nil
		}
		return m.TableDetailsGoBack()
	}
	return m, nil
}
