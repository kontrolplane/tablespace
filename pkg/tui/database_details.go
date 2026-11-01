package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/commands"
	"github.com/kontrolplane/pgui/pkg/tui/messages"
	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

type detailsTab int

const (
	tabTables detailsTab = iota
	tabDatabaseIndexes
	detailsTabCount
)

func (t detailsTab) String() string {
	if t == tabDatabaseIndexes {
		return "indexes"
	}
	return "tables"
}

type databaseDetailsState struct {
	database string
	tab      detailsTab

	tables        []pgui.Table
	tablesLoaded  bool
	tablesTable   dataTable
	selectedTable int

	indexes       []pgui.Index
	indexesLoaded bool
	indexesTable  dataTable
	selectedIndex int

	filtering   bool
	filterInput textinput.Model
	filterText  string
}

func (d *databaseDetailsState) activeTable() *dataTable {
	if d.tab == tabDatabaseIndexes {
		return &d.indexesTable
	}
	return &d.tablesTable
}

var tableColumns = []column{
	{title: "table", width: 30, grow: 1, min: 16},
	{title: "kind", width: 11},
	{title: "rows", width: 10, right: true},
	{title: "size", width: 10, right: true},
	{title: "indexes", width: 10, right: true, drop: 2},
	{title: "dead", width: 8, right: true, drop: 3},
	{title: "vacuumed", width: 10, right: true, drop: 1},
	{title: "analyzed", width: 10, right: true, drop: 4},
}

var databaseIndexColumns = []column{
	{title: "index", width: 28, grow: 1, min: 14},
	{title: "table", width: 24, grow: 1, min: 12},
	{title: "kind", width: 8},
	{title: "size", width: 10, right: true},
	{title: "scans", width: 10, right: true},
	{title: "definition", width: 30, grow: 2, drop: 1},
}

func tableMatches(t pgui.Table, filter string) bool {
	filter = strings.ToLower(filter)
	return strings.Contains(strings.ToLower(t.DisplayName()), filter) ||
		strings.Contains(strings.ToLower(t.Kind), filter) ||
		strings.Contains(strings.ToLower(t.Comment), filter)
}

func indexMatches(i pgui.Index, filter string) bool {
	filter = strings.ToLower(filter)
	return strings.Contains(strings.ToLower(i.Schema+"."+i.Table), filter) ||
		strings.Contains(strings.ToLower(i.Name), filter) ||
		strings.Contains(strings.ToLower(i.Definition), filter)
}

func (m model) getFilteredTables() []pgui.Table {
	d := m.state.databaseDetails
	return filterBy(d.tables, d.filterText, tableMatches)
}

func (m model) getFilteredIndexes() []pgui.Index {
	d := m.state.databaseDetails
	return filterBy(d.indexes, d.filterText, indexMatches)
}

func (m model) currentTable() (pgui.Table, bool) {
	ts := m.getFilteredTables()
	if len(ts) == 0 {
		return pgui.Table{}, false
	}
	return ts[m.state.databaseDetails.selectedTable], true
}

func (m model) currentIndex() (pgui.Index, bool) {
	is := m.getFilteredIndexes()
	if len(is) == 0 {
		return pgui.Index{}, false
	}
	return is[m.state.databaseDetails.selectedIndex], true
}

// estimate renders the planner's row estimate, which is all that is known of a table that was
// never counted.
func estimate(t pgui.Table) cell {
	switch {
	case t.Kind == pgui.KindView:
		return dash()
	case t.Estimate < 0:
		return text("?", styles.ToneFaint)
	}
	return cell{styles.S("~", styles.ToneFaint), styles.S(compactCount(uint64(t.Estimate)), styles.ToneText)}
}

// dash stands in for a value that does not apply.
func dash() cell { return text("-", styles.ToneFaint) }

// ago renders a time in the past, or never.
func ago(t time.Time) cell {
	if t.IsZero() {
		return text("never", styles.ToneFaint)
	}
	return text(formatAgo(t), styles.ToneMuted)
}

// tableName renders schema.name with the schema receding.
func tableName(schema, name string) cell {
	return cell{styles.S(schema+".", styles.ToneFaint), styles.B(name, styles.ToneText)}
}

func (m model) updateTablesTable() model {
	d := &m.state.databaseDetails
	var rows []tableRow
	for _, t := range m.getFilteredTables() {
		kind := text(t.Kind, styles.ToneMuted)
		if t.Partition {
			kind = text("partition", styles.ToneFaint)
		}
		size, indexes := dash(), dash()
		if t.TotalBytes > 0 {
			size = text(formatBytes(t.TotalBytes), styles.ToneBody)
		}
		if t.IndexBytes > 0 {
			indexes = text(formatBytes(t.IndexBytes), styles.ToneMuted)
		}
		dead, vacuumed, analyzed := dash(), dash(), dash()
		if t.Stats() {
			dead, vacuumed = tableCount(uint64(max(0, t.DeadRows)), styles.ToneBody), ago(t.LastVacuum)
			if t.LiveRows > 0 && t.DeadRows > t.LiveRows/5 {
				dead = text(compactCount(uint64(t.DeadRows)), styles.ToneWarning)
			}
		}
		if t.Kind != pgui.KindView {
			analyzed = ago(t.LastAnalyze)
		}
		rows = append(rows, tableRow{
			tableName(t.Schema, t.Name),
			kind,
			estimate(t),
			size,
			indexes,
			dead,
			vacuumed,
			analyzed,
		})
	}
	d.tablesTable.empty = "loading tables…"
	switch {
	case d.tablesLoaded && len(d.tables) == 0:
		d.tablesTable.empty = "no tables yet, press ctrl+n to create one"
	case d.tablesLoaded:
		d.tablesTable.empty = "no tables match the filter"
	}
	d.selectedTable = setRows(&d.tablesTable, rows, d.selectedTable)
	return m
}

// indexKind renders what an index guarantees, and when it cannot be used.
func indexKind(i pgui.Index) cell {
	switch {
	case !i.Valid:
		return text("invalid", styles.ToneDanger)
	case i.Primary:
		return text("primary", styles.ToneAccent)
	case i.Unique:
		return text("unique", styles.ToneBody)
	}
	return text("index", styles.ToneMuted)
}

// indexScans renders how often an index was used, warm when it never was.
func indexScans(i pgui.Index) cell {
	if i.Scans == 0 && !i.Primary && !i.Unique {
		return text("unused", styles.ToneWarning)
	}
	return tableCount(uint64(i.Scans), styles.ToneBody)
}

func (m model) updateDatabaseIndexesTable() model {
	d := &m.state.databaseDetails
	var rows []tableRow
	for _, i := range m.getFilteredIndexes() {
		rows = append(rows, tableRow{
			text(i.Name, styles.ToneText),
			cell{styles.S(i.Schema+".", styles.ToneFaint), styles.S(i.Table, styles.ToneBody)},
			indexKind(i),
			text(formatBytes(i.Bytes), styles.ToneMuted),
			indexScans(i),
			text(i.Method(), styles.ToneMuted),
		})
	}
	d.indexesTable.empty = "loading indexes…"
	if d.indexesLoaded {
		d.indexesTable.empty = "no indexes match the filter"
	}
	d.selectedIndex = setRows(&d.indexesTable, rows, d.selectedIndex)
	return m
}

func (m model) DatabaseDetailsSwitchPage() (model, tea.Cmd) {
	m.error = ""
	m = m.SwitchPage(databaseDetails)
	m.loading, m.loadingMsg = true, "loading tables…"
	d := &m.state.databaseDetails
	d.tab = tabTables
	d.tables, d.tablesLoaded, d.selectedTable = nil, false, 0
	d.indexes, d.indexesLoaded, d.selectedIndex = nil, false, 0
	d.tablesTable = newDataTable(tableColumns, contentWidth-4, m.overviewTableHeight())
	d.indexesTable = newDataTable(databaseIndexColumns, contentWidth-4, m.overviewTableHeight())
	d.filtering, d.filterText = false, ""
	d.filterInput.SetValue("")
	m = m.focusDetailsTab()
	return m, m.loadDetailsTab()
}

// DatabaseDetailsGoBack returns to the tables of the database and refreshes them.
func (m model) DatabaseDetailsGoBack() (model, tea.Cmd) {
	m.error = ""
	m = m.SwitchPage(databaseDetails)
	return m, m.loadDetailsTab()
}

func (m model) loadDetailsTab() tea.Cmd {
	d := m.state.databaseDetails
	if d.tab == tabDatabaseIndexes {
		return commands.LoadIndexes(m.context, m.pool, d.database)
	}
	return commands.LoadTables(m.context, m.pool, d.database)
}

func (m model) focusDetailsTab() model {
	d := &m.state.databaseDetails
	d.tablesTable.Blur()
	d.indexesTable.Blur()
	d.activeTable().Focus()
	return m
}

func (m model) applyTables(msg messages.TablesLoadedMsg) (model, tea.Cmd) {
	d := &m.state.databaseDetails
	if msg.Database != d.database {
		return m, nil
	}
	onPage := m.page == databaseDetails && d.tab == tabTables
	if onPage {
		m.loading = false
	}
	if msg.Err != nil {
		if !onPage {
			return m, nil
		}
		m, cmd := m.loadError("could not load tables", msg.Err, d.tablesLoaded)
		m, refresh := m.scheduleRefresh()
		return m, tea.Batch(cmd, refresh)
	}
	prev, ok := m.currentTable()
	d.tables, d.tablesLoaded = msg.Tables, true
	if ok {
		d.selectedTable = follow(m.getFilteredTables(), d.selectedTable, func(t pgui.Table) bool { return t.OID == prev.OID })
	}
	m = m.updateTablesTable()
	if !onPage {
		return m, nil
	}
	m.refreshedAt = time.Now()
	return m.scheduleRefresh()
}

func (m model) applyIndexes(msg messages.IndexesLoadedMsg) (model, tea.Cmd) {
	d := &m.state.databaseDetails
	if msg.Database != d.database {
		return m, nil
	}
	onPage := m.page == databaseDetails && d.tab == tabDatabaseIndexes
	if onPage {
		m.loading = false
	}
	if msg.Err != nil {
		if !onPage {
			return m, nil
		}
		m, cmd := m.loadError("could not load indexes", msg.Err, d.indexesLoaded)
		m, refresh := m.scheduleRefresh()
		return m, tea.Batch(cmd, refresh)
	}
	prev, ok := m.currentIndex()
	d.indexes, d.indexesLoaded = msg.Indexes, true
	if ok {
		d.selectedIndex = follow(m.getFilteredIndexes(), d.selectedIndex, func(i pgui.Index) bool { return i.Name == prev.Name && i.Schema == prev.Schema })
	}
	m = m.updateDatabaseIndexesTable()
	if !onPage {
		return m, nil
	}
	m.refreshedAt = time.Now()
	return m.scheduleRefresh()
}

// databaseDetailsGoBack leaves for the overview.
func (m model) databaseDetailsGoBack() (model, tea.Cmd) {
	m.state.databaseOverview.tab = tabDatabases
	m = m.focusOverviewTab()
	return m.DatabaseOverviewGoBack()
}

const createTableTemplate = `create table public.example (
  id bigint generated always as identity primary key,
  name text not null,
  created timestamptz not null default now()
);`

func (m model) DatabaseDetailsUpdate(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	d := &m.state.databaseDetails

	if d.filtering {
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(msg, m.keys.Back):
				d.filtering, d.filterText = false, ""
				d.filterInput.SetValue("")
				d.filterInput.Blur()
				return m.updateTablesTable().updateDatabaseIndexesTable(), nil
			case key.Matches(msg, m.keys.View):
				d.filtering = false
				d.filterInput.Blur()
				return m, nil
			}
		}
		d.filterInput, cmd = d.filterInput.Update(msg)
		if d.filterText != d.filterInput.Value() {
			d.filterText = d.filterInput.Value()
			d.selectedTable, d.selectedIndex = 0, 0
		}
		return m.updateTablesTable().updateDatabaseIndexesTable(), cmd
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
		d.tab = detailsTab(nextTab(int(d.tab), delta, int(detailsTabCount)))
		m.refreshedAt = time.Time{}
		m = m.focusDetailsTab()
		return m, m.loadDetailsTab()
	case key.Matches(keyMsg, m.keys.Filter):
		d.filtering = true
		d.filterInput.Placeholder = map[detailsTab]string{
			tabTables:          "filter by schema, name, kind or comment…",
			tabDatabaseIndexes: "filter by table, name or definition…",
		}[d.tab]
		return m, d.filterInput.Focus()
	case key.Matches(keyMsg, m.keys.Refresh):
		if m.disconnected() {
			return m.setStatus(offlineStatus, styles.ToneWarning)
		}
		return m, m.loadDetailsTab()
	case key.Matches(keyMsg, m.keys.Editor):
		return m.SQLEditorSwitchPage("")
	case key.Matches(keyMsg, m.keys.Create):
		if m, cmd, refused := m.refuseReadOnly(); refused {
			return m, cmd
		}
		return m.SQLEditorSwitchPage(createTableTemplate)
	case key.Matches(keyMsg, m.keys.Back), key.Matches(keyMsg, m.keys.Quit):
		if d.filterText != "" {
			d.filterText = ""
			d.filterInput.SetValue("")
			return m.updateTablesTable().updateDatabaseIndexesTable(), nil
		}
		return m.databaseDetailsGoBack()
	}

	if d.tab == tabDatabaseIndexes {
		switch {
		case key.Matches(keyMsg, m.keys.CopyToClipboard):
			if i, ok := m.currentIndex(); ok {
				return m, commands.CopyToClipboard(i.Definition)
			}
		case key.Matches(keyMsg, m.keys.View):
			if i, ok := m.currentIndex(); ok {
				for _, t := range d.tables {
					if t.Schema == i.Schema && t.Name == i.Table {
						return m.TableDetailsSwitchPage(t)
					}
				}
				return m.setStatus("the table of "+i.Name+" is not listed, open the tables tab first", styles.ToneWarning)
			}
		default:
			d.indexesTable = d.indexesTable.Update(keyMsg)
			d.selectedIndex = d.indexesTable.Cursor()
		}
		return m, nil
	}

	switch {
	case key.Matches(keyMsg, m.keys.View):
		if t, ok := m.currentTable(); ok {
			return m.TableDetailsSwitchPage(t)
		}
	case key.Matches(keyMsg, m.keys.CopyToClipboard):
		if t, ok := m.currentTable(); ok {
			return m, commands.CopyToClipboard(t.QualifiedName())
		}
	default:
		d.tablesTable = d.tablesTable.Update(keyMsg)
		d.selectedTable = d.tablesTable.Cursor()
	}
	return m, nil
}

func (m model) DatabaseDetailsView() string {
	d := &m.state.databaseDetails
	tabs := renderTabs([]tabItem{
		{name: "tables", count: len(d.tablesTable.rows), loaded: d.tablesLoaded},
		{name: "indexes", count: len(d.indexesTable.rows), loaded: d.indexesLoaded},
	}, int(d.tab), m.databaseDetailsHint()...)
	return tabs + "\n\n" + lipgloss.PlaceHorizontal(contentWidth-4, lipgloss.Left, d.activeTable().View())
}

// unusedIndexHint says what an unused index is, in a long and a short wording.
var unusedIndexHint = []string{"unused indexes were never scanned since the stats were reset, yet slow down writes", "unused: never scanned"}

func (m model) databaseDetailsHint() []string {
	if m.state.databaseDetails.tab == tabDatabaseIndexes {
		return unusedIndexHint
	}
	return []string{"rows are the planner's estimate from the last analyze", "rows are estimates"}
}
