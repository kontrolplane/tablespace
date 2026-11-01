package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kontrolplane/tablespace/pkg/tablespace"
	"github.com/kontrolplane/tablespace/pkg/tui/commands"
	"github.com/kontrolplane/tablespace/pkg/tui/messages"
	"github.com/kontrolplane/tablespace/pkg/tui/styles"
)

type tableTab int

const (
	tabRows tableTab = iota
	tabColumns
	tabIndexes
	tabConstraints
	tableTabCount
)

func (t tableTab) String() string {
	return [...]string{"rows", "columns", "indexes", "constraints"}[t]
}

type tableDetailsState struct {
	table        tablespace.Table
	attributes   string
	tab          tableTab
	schemaLoaded bool

	columns        []tablespace.Column
	columnsTable   dataTable
	selectedColumn int

	indexes       []tablespace.Index
	indexesTable  dataTable
	selectedIndex int

	constraints        []tablespace.Constraint
	constraintsTable   dataTable
	selectedConstraint int

	paging       tablespace.Paging
	fields       []tablespace.Field
	rows         []tablespace.Row
	rowsLoaded   bool
	rowsTable    dataTable
	selectedRow  int
	selectedRows map[string]bool // keys of the rows selected for bulk operations
	next         []string        // where the next older page starts, nil when there is none
	paged        bool            // older pages were added below the newest rows
	loadingOlder bool
	colOffset    int  // the first column shown, ←/→ scroll through them
	follow       bool // the cursor stays on the newest row as new ones arrive

	where        string
	whereEditing bool
	whereInput   textinput.Model

	filtering   bool
	filterInput textinput.Model
	filterText  string
}

// following reports whether the cursor stays on the newest row: follow is on, the cursor is on
// the top row and the rows have an order to tell the newest by.
func (td *tableDetailsState) following() bool {
	return td.follow && td.selectedRow == 0 && !td.paging.ByOffset()
}

func (td *tableDetailsState) activeTable() *dataTable {
	switch td.tab {
	case tabColumns:
		return &td.columnsTable
	case tabIndexes:
		return &td.indexesTable
	case tabConstraints:
		return &td.constraintsTable
	}
	return &td.rowsTable
}

var columnColumns = []column{
	{title: "column", width: 24, grow: 1, min: 12},
	{title: "type", width: 24},
	{title: "null", width: 4},
	{title: "default", width: 30, grow: 1, drop: 1},
	{title: "comment", width: 24, grow: 1, drop: 2},
}

var tableIndexColumns = []column{
	{title: "index", width: 28, grow: 1, min: 14},
	{title: "kind", width: 8},
	{title: "size", width: 10, right: true},
	{title: "scans", width: 10, right: true},
	{title: "definition", width: 50, grow: 2, drop: 1},
}

var constraintColumns = []column{
	{title: "constraint", width: 28, grow: 1, min: 14},
	{title: "kind", width: 11},
	{title: "definition", width: 60, grow: 2},
}

func initWhereInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "a condition in sql, like status = 'open' and total > 100"
	ti.Prompt = ""
	ti.CharLimit = 2000
	ti.SetWidth(70)
	ti.SetStyles(styles.TextInput())
	return ti
}

func sep() styles.Span { return styles.S(" · ", styles.ToneFaint) }

type attribute struct {
	label string
	value cell
}

const attributeLabelWidth = 14

func attributeColumn(width int, rows []attribute) string {
	label := styles.Fg(styles.ToneFaint).Width(attributeLabelWidth)
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = label.Render(r.label) + renderCell(r.value, column{width: width - attributeLabelWidth}, nil)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func renderTableAttributes(t tablespace.Table, p tablespace.Paging, schemaLoaded bool) string {
	kind := cell{styles.S(t.Kind, styles.ToneBody), sep(), styles.S("owner ", styles.ToneFaint), styles.S(t.Owner, styles.ToneBody)}
	if t.Partition {
		kind = append(cell{styles.S("partition of a ", styles.ToneFaint)}, kind...)
	}
	rows := dash()
	if t.Kind != tablespace.KindView {
		rows = append(cell{styles.S("estimate ", styles.ToneFaint)}, estimate(t)...)
	}
	if t.Stats() {
		rows = append(rows, sep(), styles.S(compactCount(uint64(max(0, t.LiveRows)))+" live", styles.ToneBody),
			sep(), styles.S(compactCount(uint64(max(0, t.DeadRows)))+" dead", styles.ToneMuted))
	}
	size := dash()
	if t.TotalBytes > 0 {
		size = cell{styles.B(formatBytes(t.TotalBytes), styles.ToneText), sep(),
			styles.S("table ", styles.ToneFaint), styles.S(formatBytes(t.TableBytes), styles.ToneBody), sep(),
			styles.S("indexes ", styles.ToneFaint), styles.S(formatBytes(t.IndexBytes), styles.ToneBody)}
	}
	if t.Kind == tablespace.KindPartitioned {
		size = append(size, styles.S("  in its partitions", styles.ToneFaint))
	}
	stat := func(c cell) cell {
		if !t.Stats() {
			return dash()
		}
		return c
	}
	analyzed := dash()
	if t.Kind != tablespace.KindView {
		analyzed = ago(t.LastAnalyze)
	}
	order := text("…", styles.ToneFaint)
	if schemaLoaded {
		order = text(p.Describe(), styles.ToneBody)
		if !p.Deletable() && t.HoldsRows() {
			order = append(order, sep(), styles.S("rows cannot be deleted here", styles.ToneWarning))
		}
	}

	leftWidth := (contentWidth - 6) * 55 / 100
	left := attributeColumn(leftWidth, []attribute{
		{"kind", kind},
		{"rows", rows},
		{"size", size},
		{"order", order},
		{"comment", text(orDash(t.Comment), styles.ToneMuted)},
	})
	right := attributeColumn(contentWidth-6-leftWidth, []attribute{
		{"scans", stat(cell{styles.S("seq ", styles.ToneFaint), styles.S(compactCount(uint64(t.SeqScans)), styles.ToneBody), sep(), styles.S("index ", styles.ToneFaint), styles.S(compactCount(uint64(t.IdxScans)), styles.ToneBody)})},
		{"writes", stat(cell{styles.S("ins ", styles.ToneFaint), styles.S(compactCount(uint64(t.Inserted)), styles.ToneBody), sep(), styles.S("upd ", styles.ToneFaint), styles.S(compactCount(uint64(t.Updated)), styles.ToneBody), sep(), styles.S("del ", styles.ToneFaint), styles.S(compactCount(uint64(t.Deleted)), styles.ToneBody)})},
		{"vacuumed", stat(ago(t.LastVacuum))},
		{"analyzed", analyzed},
	})
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
}

// rowKey is the key a row is selected by.
func rowKey(r tablespace.Row) string { return strings.Join(r.Key, "\x00") }

// The type oids that are drawn differently.
const (
	oidBool  = 16
	oidBytea = 17
	oidJSON  = 114
	oidJSONB = 3802
)

// numeric reports whether values of the type read as numbers, and are aligned to the right.
func numeric(oid uint32) bool {
	switch oid {
	case 20, 21, 23, 26, 700, 701, 790, 1700:
		return true
	}
	return false
}

func isJSONType(oid uint32) bool { return oid == oidJSON || oid == oidJSONB }

// maxCellText bounds the text a cell is built from, a cell shows a line at most.
const maxCellText = 512

// cellText puts a value on one line for a cell, reading only as much of it as a cell can show.
func cellText(s string) string {
	if len(s) > maxCellText {
		cut := maxCellText
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return strings.Join(strings.Fields(s), " ")
}

// valueCell renders a value in a table: null and the empty string recede, JSON is tinted.
func valueCell(v tablespace.Value, oid uint32) cell {
	switch {
	case v.Null:
		return text("null", styles.ToneFaint)
	case v.Text == "":
		return text(`""`, styles.ToneFaint)
	case isJSONType(oid):
		return tintJSON(styles.Clean(cellText(v.Text)))
	case oid == oidBytea:
		return text(cellText(v.Text), styles.ToneMuted)
	case oid == oidBool:
		if v.Text == "t" {
			return text("true", styles.ToneBody)
		}
		return text("false", styles.ToneMuted)
	}
	return text(cellText(v.Text), styles.ToneBody)
}

// fieldWidth is the width a column of rows gets: what its title and the longest of its values
// need, within bounds.
func fieldWidth(i int, f tablespace.Field, rows []tablespace.Row) int {
	w := textWidth(f.Name)
	for _, r := range rows {
		if i < len(r.Values) {
			w = max(w, min(40, textWidth(cellText(r.Values[i].Text))))
		}
	}
	return max(4, min(40, w))
}

func rowMatches(r tablespace.Row, filter string) bool {
	filter = strings.ToLower(filter)
	for _, v := range r.Values {
		if !v.Null && strings.Contains(strings.ToLower(v.Text), filter) {
			return true
		}
	}
	return false
}

func (m model) getFilteredRows() []tablespace.Row {
	td := m.state.tableDetails
	return filterBy(td.rows, td.filterText, rowMatches)
}

func (m model) currentRow() (tablespace.Row, bool) {
	rows := m.getFilteredRows()
	if len(rows) == 0 {
		return tablespace.Row{}, false
	}
	return rows[m.state.tableDetails.selectedRow], true
}

// updateRowsTable builds the rows table from the column the view is scrolled to. Columns that do
// not fit are hidden from the right.
func (m model) updateRowsTable() model {
	td := &m.state.tableDetails
	td.colOffset = max(0, min(td.colOffset, len(td.fields)-1))
	var cols []column
	for i, f := range td.fields[td.colOffset:] {
		c := column{title: f.Name, width: fieldWidth(td.colOffset+i, f, td.rows), right: numeric(f.Type), key: slices.Contains(td.paging.Key, f.Name)}
		if i > 0 {
			c.drop = i
		} else if td.paging.Deletable() {
			c.width += 2 // room for the mark of a selected row
		}
		cols = append(cols, c)
	}
	// A left aligned last column takes the width left over, a number would drift away from the rest.
	if n := len(cols); n > 0 && !cols[n-1].right {
		cols[n-1].grow, cols[n-1].min = 1, cols[n-1].width
	}
	cursor := td.selectedRow
	td.rowsTable = newDataTable(cols, contentWidth-4, m.getDetailsTableHeight())
	if td.tab != tabRows {
		td.rowsTable.Blur()
	}

	var rows []tableRow
	for _, r := range m.getFilteredRows() {
		row := make(tableRow, 0, len(cols))
		for i, v := range r.Values[min(td.colOffset, len(r.Values)):] {
			c := valueCell(v, td.fields[td.colOffset+i].Type)
			if i == 0 && td.selectedRows[rowKey(r)] {
				c = append(cell{styles.S("● ", styles.ToneAccent)}, c...)
			}
			row = append(row, c)
		}
		rows = append(rows, row)
	}
	td.rowsTable.empty = "loading rows…"
	switch {
	case td.rowsLoaded && td.where != "" && len(td.rows) == 0:
		td.rowsTable.empty = "no rows match the where clause, w changes it"
	case td.rowsLoaded && len(td.rows) == 0:
		td.rowsTable.empty = "no rows yet"
	case td.rowsLoaded:
		td.rowsTable.empty = "no loaded rows match the search"
	}
	td.selectedRow = setRows(&td.rowsTable, rows, cursor)
	return m
}

// visibleColumns returns the range of columns the rows table shows.
func (td *tableDetailsState) visibleColumns() (first, last int) {
	shown := 0
	for _, c := range td.rowsTable.columns {
		if !c.hide {
			shown++
		}
	}
	return td.colOffset, td.colOffset + shown - 1
}

func (m model) getFilteredColumns() []tablespace.Column {
	td := m.state.tableDetails
	return filterBy(td.columns, td.filterText, func(c tablespace.Column, f string) bool {
		return strings.Contains(strings.ToLower(c.Name+" "+c.Type+" "+c.Comment), strings.ToLower(f))
	})
}

func (m model) getFilteredConstraints() []tablespace.Constraint {
	td := m.state.tableDetails
	return filterBy(td.constraints, td.filterText, func(c tablespace.Constraint, f string) bool {
		return strings.Contains(strings.ToLower(c.Name+" "+c.Kind+" "+c.Definition), strings.ToLower(f))
	})
}

func (m model) updateColumnsTable() model {
	td := &m.state.tableDetails
	var rows []tableRow
	for _, c := range m.getFilteredColumns() {
		null := text("yes", styles.ToneMuted)
		if !c.Nullable {
			null = text("no", styles.ToneBody)
		}
		def := text(orDash(c.Default), styles.ToneMuted)
		switch {
		case c.Identity != "":
			def = cell{styles.S("identity ", styles.ToneAccent), styles.S(c.Identity, styles.ToneMuted)}
		case c.Generated:
			def = cell{styles.S("generated ", styles.ToneAccent), styles.S(c.Default, styles.ToneMuted)}
		case c.Default == "":
			def = text("-", styles.ToneFaint)
		}
		name := text(c.Name, styles.ToneText)
		if slices.Contains(td.paging.Key, c.Name) {
			name = text(c.Name, styles.ToneAccent)
		}
		rows = append(rows, tableRow{
			name,
			text(c.Type, styles.ToneBody),
			null,
			def,
			text(orDash(c.Comment), styles.ToneFaint),
		})
	}
	td.columnsTable.empty = "loading columns…"
	if td.schemaLoaded {
		td.columnsTable.empty = "no columns match the filter"
	}
	td.selectedColumn = setRows(&td.columnsTable, rows, td.selectedColumn)
	return m
}

func (m model) updateTableIndexesTable() model {
	td := &m.state.tableDetails
	var rows []tableRow
	for _, i := range filterBy(td.indexes, td.filterText, indexMatches) {
		rows = append(rows, tableRow{
			text(i.Name, styles.ToneText),
			indexKind(i),
			text(formatBytes(i.Bytes), styles.ToneMuted),
			indexScans(i),
			text(i.Method(), styles.ToneMuted),
		})
	}
	td.indexesTable.empty = "loading indexes…"
	switch {
	case td.schemaLoaded && len(td.indexes) == 0:
		td.indexesTable.empty = "no indexes"
	case td.schemaLoaded:
		td.indexesTable.empty = "no indexes match the filter"
	}
	td.selectedIndex = setRows(&td.indexesTable, rows, td.selectedIndex)
	return m
}

func (m model) updateConstraintsTable() model {
	td := &m.state.tableDetails
	var rows []tableRow
	for _, c := range m.getFilteredConstraints() {
		kind := text(c.Kind, styles.ToneMuted)
		if c.Kind == "primary key" {
			kind = text(c.Kind, styles.ToneAccent)
		}
		rows = append(rows, tableRow{
			text(c.Name, styles.ToneText),
			kind,
			text(c.Definition, styles.ToneBody),
		})
	}
	td.constraintsTable.empty = "loading constraints…"
	switch {
	case td.schemaLoaded && len(td.constraints) == 0:
		td.constraintsTable.empty = "no constraints"
	case td.schemaLoaded:
		td.constraintsTable.empty = "no constraints match the filter"
	}
	td.selectedConstraint = setRows(&td.constraintsTable, rows, td.selectedConstraint)
	return m
}

func (m model) updateTableTabs() model {
	return m.updateRowsTable().updateColumnsTable().updateTableIndexesTable().updateConstraintsTable()
}

func (m model) TableDetailsSwitchPage(t tablespace.Table) (model, tea.Cmd) {
	m.error = ""
	m = m.SwitchPage(tableDetails)
	m.loading, m.loadingMsg = true, "loading "+t.DisplayName()+"…"
	td := &m.state.tableDetails
	*td = tableDetailsState{
		table:        t,
		tab:          tabRows,
		selectedRows: make(map[string]bool),
		filterInput:  td.filterInput,
		whereInput:   td.whereInput,
		follow:       true,
	}
	td.filterInput.SetValue("")
	td.whereInput.SetValue("")
	td.columnsTable = newDataTable(columnColumns, contentWidth-4, m.getDetailsTableHeight())
	td.indexesTable = newDataTable(tableIndexColumns, contentWidth-4, m.getDetailsTableHeight())
	td.constraintsTable = newDataTable(constraintColumns, contentWidth-4, m.getDetailsTableHeight())
	td.attributes = renderTableAttributes(t, td.paging, false)
	m = m.updateTableTabs().focusTableTab()
	return m, tea.Batch(commands.LoadTable(m.context, m.pool, t.OID), commands.LoadSchema(m.context, m.pool, t))
}

// TableDetailsGoBack returns to the table and refreshes it in the background.
func (m model) TableDetailsGoBack() (model, tea.Cmd) {
	m.error = ""
	m = m.SwitchPage(tableDetails)
	return m.refreshTableDetails()
}

// refreshTableDetails reloads the stats of the table, and its newest rows unless older pages were
// loaded below them, which a reload would take away.
func (m model) refreshTableDetails() (model, tea.Cmd) {
	td := m.state.tableDetails
	cmds := []tea.Cmd{commands.LoadTable(m.context, m.pool, td.table.OID)}
	if td.schemaLoaded && td.tab == tabRows && !td.paged && !td.loadingOlder {
		var cmd tea.Cmd
		m, cmd = m.loadRows(nil)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// resetRows forgets the loaded rows, for a load of the newest ones in their place.
func (m model) resetRows() model {
	td := &m.state.tableDetails
	td.rows, td.rowsLoaded, td.next, td.paged, td.loadingOlder = nil, false, nil, false, false
	td.selectedRow = 0
	return m.updateRowsTable()
}

// loadRows loads the page of rows starting at after, the newest when it is nil.
func (m model) loadRows(after []string) (model, tea.Cmd) {
	td := &m.state.tableDetails
	m.rowsGen++
	td.loadingOlder = after != nil
	return m, commands.LoadRows(m.context, m.pool, td.table, td.paging, td.where, after, m.rowsGen)
}

func (m model) focusTableTab() model {
	td := &m.state.tableDetails
	for _, t := range []*dataTable{&td.rowsTable, &td.columnsTable, &td.indexesTable, &td.constraintsTable} {
		t.Blur()
	}
	td.activeTable().Focus()
	return m
}

// tableGone leaves a table that was dropped elsewhere.
func (m model) tableGone(t tablespace.Table) (model, tea.Cmd) {
	m.loading = false
	d := &m.state.databaseDetails
	d.tables = deleteNames(d.tables, []uint32{t.OID}, func(t tablespace.Table) uint32 { return t.OID })
	m = m.updateTablesTable()
	m, cmd := m.DatabaseDetailsGoBack()
	return m.withStatus(cmd, t.Kind+" "+t.DisplayName()+" no longer exists", styles.ToneDanger)
}

// inTable reports whether the current page shows the table held in the details state.
func (m model) inTable() bool {
	switch m.page {
	case tableDetails, tableDrop, rowDetails, rowDelete:
		return true
	}
	return false
}

func (m model) applyTable(msg messages.TableLoadedMsg) (model, tea.Cmd) {
	td := &m.state.tableDetails
	if msg.OID != td.table.OID || !m.inTable() {
		return m, nil
	}
	switch {
	case errors.Is(msg.Err, tablespace.ErrTableNotFound) && !m.busy:
		return m.tableGone(td.table)
	case msg.Err != nil:
		if m.page != tableDetails {
			return m, nil
		}
		m.loading = false
		m, cmd := m.loadError("could not load "+td.table.DisplayName(), msg.Err, td.schemaLoaded)
		m, refresh := m.scheduleRefresh()
		return m, tea.Batch(cmd, refresh)
	}
	td.table = msg.Table
	td.attributes = renderTableAttributes(td.table, td.paging, td.schemaLoaded)
	if m.page != tableDetails {
		return m, nil
	}
	m.refreshedAt = time.Now()
	return m.scheduleRefresh()
}

func (m model) applySchema(msg messages.SchemaLoadedMsg) (model, tea.Cmd) {
	td := &m.state.tableDetails
	if msg.OID != td.table.OID || !m.inTable() {
		return m, nil
	}
	if msg.Err != nil {
		m.loading = false
		return m.loadError("could not load the columns of "+td.table.DisplayName(), msg.Err, false)
	}
	td.columns, td.indexes, td.constraints, td.paging = msg.Columns, msg.Indexes, msg.Constraints, msg.Paging
	td.schemaLoaded = true
	td.attributes = renderTableAttributes(td.table, td.paging, true)
	m = m.updateTableTabs()
	return m.loadRows(nil)
}

func (m model) applyRows(msg messages.RowsLoadedMsg) (model, tea.Cmd) {
	td := &m.state.tableDetails
	if msg.OID != td.table.OID || !m.inTable() || msg.Gen < m.rowsApplied {
		return m, nil
	}
	m.rowsApplied = msg.Gen
	if m.page == tableDetails {
		m.loading = false
	}
	older := msg.After != nil
	td.loadingOlder = false
	if msg.Err != nil {
		return m.loadError("could not load rows", msg.Err, td.rowsLoaded && !older)
	}
	page := msg.Page
	if older {
		td.rows = append(slices.Clone(td.rows), page.Rows...)
		td.paged = true
	} else {
		prev, ok := m.currentRow()
		td.rows = page.Rows
		if !td.following() && ok {
			td.selectedRow = follow(m.getFilteredRows(), td.selectedRow, func(r tablespace.Row) bool { return rowKey(r) == rowKey(prev) })
		}
		prune(td.selectedRows, td.rows, rowKey)
	}
	td.fields, td.next, td.rowsLoaded = page.Fields, page.Next, true
	return m.updateRowsTable(), nil
}

// rowsFootMeta says what the frame shows of the rows: whether it follows the newest, which
// columns are in sight and whether older rows are left to load.
func (m model) rowsFootMeta() []string {
	td := &m.state.tableDetails
	var foot []string
	if !td.paging.ByOffset() && td.schemaLoaded {
		if td.following() {
			foot = append(foot, styles.Render(styles.S("● ", styles.ToneAccent), styles.S("following", styles.ToneMuted)))
		} else {
			foot = append(foot, styles.Faint("follow off"))
		}
	}
	if first, last := td.visibleColumns(); len(td.fields) > 0 && (first > 0 || last < len(td.fields)-1) {
		foot = append(foot, styles.Faint(fmt.Sprintf("columns %d–%d of %d", first+1, last+1, len(td.fields))))
	}
	switch {
	case td.loadingOlder:
		foot = append(foot, styles.Faint("loading older rows…"))
	case td.next != nil:
		foot = append(foot, styles.Faint("more below"))
	}
	return foot
}

// rowJSON writes a row out as a JSON object, its columns in order. JSON values stay JSON.
func rowJSON(fields []tablespace.Field, r tablespace.Row) string {
	var b strings.Builder
	b.WriteString("{")
	for i, f := range fields {
		if i >= len(r.Values) {
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		name, _ := json.Marshal(f.Name)
		b.Write(name)
		b.WriteString(": ")
		v := r.Values[i]
		switch {
		case v.Null:
			b.WriteString("null")
		case isJSONType(f.Type) && json.Valid([]byte(v.Text)):
			b.WriteString(v.Text)
		default:
			s, _ := json.Marshal(v.Text)
			b.Write(s)
		}
	}
	b.WriteString("}")
	return b.String()
}

// tableDetailsGoBack leaves for the tables of the database.
func (m model) tableDetailsGoBack() (model, tea.Cmd) {
	m.state.databaseDetails.tab = tabTables
	m = m.focusDetailsTab()
	return m.DatabaseDetailsGoBack()
}

// clearTableFilterOrSelection clears the search, or else the selection of rows, and reports
// whether there was either to clear.
func (m model) clearTableFilterOrSelection() (model, bool) {
	td := &m.state.tableDetails
	switch {
	case td.filterText != "":
		td.filterText = ""
		td.filterInput.SetValue("")
	case td.tab == tabRows && len(td.selectedRows) > 0:
		td.selectedRows = make(map[string]bool)
	default:
		return m, false
	}
	return m.updateTableTabs(), true
}

func (m model) TableDetailsUpdate(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	td := &m.state.tableDetails

	if td.whereEditing {
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(msg, m.keys.Back):
				td.whereEditing = false
				td.whereInput.SetValue(td.where)
				td.whereInput.Blur()
				return m, nil
			case key.Matches(msg, m.keys.View):
				td.whereEditing = false
				td.whereInput.Blur()
				td.where = strings.TrimSpace(td.whereInput.Value())
				m = m.resetRows()
				return m.loadRows(nil)
			}
		}
		td.whereInput, cmd = td.whereInput.Update(msg)
		return m, cmd
	}

	if td.filtering {
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(msg, m.keys.Back):
				td.filtering, td.filterText = false, ""
				td.filterInput.SetValue("")
				td.filterInput.Blur()
				return m.updateTableTabs(), nil
			case key.Matches(msg, m.keys.View):
				td.filtering = false
				td.filterInput.Blur()
				return m, nil
			}
		}
		td.filterInput, cmd = td.filterInput.Update(msg)
		if td.filterText != td.filterInput.Value() {
			td.filterText = td.filterInput.Value()
			td.selectedRow, td.selectedColumn, td.selectedIndex, td.selectedConstraint = 0, 0, 0, 0
		}
		return m.updateTableTabs(), cmd
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(keyMsg, m.keys.SwitchTab), key.Matches(keyMsg, m.keys.PrevTab):
		delta := 1
		if key.Matches(keyMsg, m.keys.PrevTab) {
			delta = -1
		}
		td.tab = tableTab(nextTab(int(td.tab), delta, int(tableTabCount)))
		return m.focusTableTab(), nil
	case key.Matches(keyMsg, m.keys.Filter):
		td.filtering = true
		td.filterInput.Placeholder = map[tableTab]string{
			tabRows:        "search the loaded rows…",
			tabColumns:     "filter by name, type or comment…",
			tabIndexes:     "filter by name or definition…",
			tabConstraints: "filter by name, kind or definition…",
		}[td.tab]
		return m, td.filterInput.Focus()
	case key.Matches(keyMsg, m.keys.Refresh):
		if m.disconnected() {
			return m.setStatus(offlineStatus, styles.ToneWarning)
		}
		cmds := []tea.Cmd{commands.LoadTable(m.context, m.pool, td.table.OID)}
		if td.schemaLoaded {
			m = m.resetRows()
			m, cmd = m.loadRows(nil)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case key.Matches(keyMsg, m.keys.Editor):
		return m.SQLEditorSwitchPage(fmt.Sprintf("select *\nfrom %s\nlimit 100;", td.table.QualifiedName()))
	case key.Matches(keyMsg, m.keys.Back):
		if m, ok := m.clearTableFilterOrSelection(); ok {
			return m, nil
		}
		return m.tableDetailsGoBack()
	case key.Matches(keyMsg, m.keys.Quit):
		if m, ok := m.clearTableFilterOrSelection(); ok {
			return m, nil
		}
		return m.tableDetailsGoBack()
	case key.Matches(keyMsg, m.keys.Truncate):
		return m.tableDropOpen(true)
	case key.Matches(keyMsg, m.keys.Delete) && td.tab != tabRows:
		return m.tableDropOpen(false)
	}

	switch td.tab {
	case tabRows:
		return m.rowsKey(keyMsg)
	case tabColumns:
		if key.Matches(keyMsg, m.keys.CopyToClipboard) {
			if cs := m.getFilteredColumns(); td.selectedColumn < len(cs) {
				return m, commands.CopyToClipboard(cs[td.selectedColumn].Name)
			}
			return m, nil
		}
	case tabIndexes:
		if key.Matches(keyMsg, m.keys.CopyToClipboard) {
			if is := filterBy(td.indexes, td.filterText, indexMatches); td.selectedIndex < len(is) {
				return m, commands.CopyToClipboard(is[td.selectedIndex].Definition)
			}
			return m, nil
		}
	case tabConstraints:
		if key.Matches(keyMsg, m.keys.CopyToClipboard) {
			if cs := m.getFilteredConstraints(); td.selectedConstraint < len(cs) {
				return m, commands.CopyToClipboard(cs[td.selectedConstraint].Definition)
			}
			return m, nil
		}
	}
	t := td.activeTable()
	*t = t.Update(keyMsg)
	switch td.tab {
	case tabColumns:
		td.selectedColumn = t.Cursor()
	case tabIndexes:
		td.selectedIndex = t.Cursor()
	case tabConstraints:
		td.selectedConstraint = t.Cursor()
	}
	return m, nil
}

func (m model) rowsKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	td := &m.state.tableDetails
	switch {
	case key.Matches(msg, m.keys.View):
		if r, ok := m.currentRow(); ok {
			return m.RowDetailsSwitchPage(r)
		}
	case key.Matches(msg, m.keys.Select):
		if r, ok := m.currentRow(); ok {
			toggle(td.selectedRows, rowKey(r))
		}
		return m.updateRowsTable(), nil
	case key.Matches(msg, m.keys.CopyToClipboard):
		if r, ok := m.currentRow(); ok {
			return m, commands.CopyToClipboard(rowJSON(td.fields, r))
		}
	case key.Matches(msg, m.keys.Where):
		if !td.schemaLoaded {
			return m, nil
		}
		td.whereEditing = true
		td.whereInput.SetValue(td.where)
		td.whereInput.CursorEnd()
		return m, td.whereInput.Focus()
	case key.Matches(msg, m.keys.Follow):
		if td.paging.ByOffset() {
			return m.setStatus("the rows have no order to follow the newest by", styles.ToneWarning)
		}
		if td.following() {
			td.follow = false
			return m, nil
		}
		td.follow = true
		if td.paged || td.selectedRow > 0 {
			m = m.resetRows()
			return m.loadRows(nil)
		}
		return m, nil
	case key.Matches(msg, m.keys.Left):
		if td.colOffset > 0 {
			td.colOffset--
			return m.updateRowsTable(), nil
		}
	case key.Matches(msg, m.keys.Right):
		if _, last := td.visibleColumns(); last < len(td.fields)-1 {
			td.colOffset++
			return m.updateRowsTable(), nil
		}
	case key.Matches(msg, m.keys.Delete):
		return m.rowDeleteOpen(false)
	default:
		atEnd := td.selectedRow == len(td.rowsTable.rows)-1
		td.rowsTable = td.rowsTable.Update(msg)
		td.selectedRow = td.rowsTable.Cursor()
		if td.selectedRow > 0 {
			td.follow = false
		}
		down := msg.String() == "down" || msg.String() == "j" || msg.String() == "pgdown" || msg.String() == "G" || msg.String() == "end"
		if down && (atEnd || td.selectedRow == len(td.rowsTable.rows)-1) && td.next != nil && !td.loadingOlder && td.filterText == "" {
			return m.loadRows(td.next)
		}
	}
	return m, nil
}

func (m model) renderTableTabBar() string {
	td := &m.state.tableDetails
	tabs := []tabItem{
		{name: "rows", count: len(td.rowsTable.rows), loaded: td.rowsLoaded, more: td.next != nil},
		{name: "columns", count: len(td.columnsTable.rows), loaded: td.schemaLoaded},
		{name: "indexes", count: len(td.indexesTable.rows), loaded: td.schemaLoaded},
		{name: "constraints", count: len(td.constraintsTable.rows), loaded: td.schemaLoaded},
	}
	var hint []string
	switch {
	case td.tab == tabRows && td.next != nil:
		hint = []string{"older rows load as you move past the last one", "more load past the last"}
	case td.tab == tabRows && td.rowsLoaded:
		hint = []string{"every row is loaded", "all loaded"}
	case td.tab == tabIndexes:
		hint = unusedIndexHint
	}
	return renderTabs(tabs, int(td.tab), hint...)
}

func (m model) TableDetailsView() string {
	td := &m.state.tableDetails
	attributes := td.attributes
	if attributes == "" {
		attributes = styles.Muted("loading table attributes…")
	}
	return lipgloss.PlaceHorizontal(contentWidth-4, lipgloss.Left, attributes) + "\n\n" +
		m.renderTableTabBar() + "\n\n" +
		lipgloss.PlaceHorizontal(contentWidth-4, lipgloss.Left, td.activeTable().View())
}
