package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kontrolplane/pgui/pkg/client"
	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/commands"
	"github.com/kontrolplane/pgui/pkg/tui/messages"
	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

type overviewTab int

const (
	tabDatabases overviewTab = iota
	tabActivity
	overviewTabCount
)

func (t overviewTab) String() string {
	if t == tabActivity {
		return "activity"
	}
	return "databases"
}

type databaseOverviewState struct {
	tab overviewTab

	databases         []pgui.Database
	databasesLoaded   bool
	databasesTable    dataTable
	selectedDatabase  int
	selectedDatabases map[string]bool // database names selected for bulk operations
	cursorTo          string          // the database the cursor goes to once it is listed

	backends         []pgui.Backend
	backendsLoaded   bool
	backendsTable    dataTable
	selectedBackend  int
	selectedBackends map[int]bool // pids selected for bulk operations

	filtering   bool
	filterInput textinput.Model
	filterText  string
}

// activeTable returns the table of the open tab.
func (o *databaseOverviewState) activeTable() *dataTable {
	if o.tab == tabActivity {
		return &o.backendsTable
	}
	return &o.databasesTable
}

var databaseColumns = []column{
	{title: "database", width: 24, grow: 1, min: 14},
	{title: "owner", width: 14},
	{title: "encoding", width: 8, drop: 2},
	{title: "collation", width: 12, drop: 3},
	{title: "size", width: 10, right: true},
	{title: "connections", width: 11, right: true},
	{title: "comment", width: 24, grow: 1, drop: 1},
}

var backendColumns = []column{
	{title: "pid", width: 9, right: true},
	{title: "user", width: 12, drop: 3},
	{title: "database", width: 14},
	{title: "application", width: 16, drop: 2},
	{title: "client", width: 16, drop: 4},
	{title: "state", width: 14},
	{title: "waiting on", width: 16, drop: 1},
	{title: "duration", width: 8, right: true},
	{title: "query", width: 30, grow: 1, min: 16},
}

// appName is the application name pgui connects with, which tells its own sessions apart.
func (m model) appName() string {
	return m.projectName + "/" + m.programName
}

func databaseMatches(d pgui.Database, filter string) bool {
	filter = strings.ToLower(filter)
	return strings.Contains(strings.ToLower(d.Name), filter) ||
		strings.Contains(strings.ToLower(d.Owner), filter) ||
		strings.Contains(strings.ToLower(d.Comment), filter)
}

func backendMatches(b pgui.Backend, filter string) bool {
	filter = strings.ToLower(filter)
	if strconv.Itoa(b.PID) == filter {
		return true
	}
	for _, s := range []string{b.User, b.Database, b.Application, b.Client, b.State, b.Query, b.Type, b.WaitEvent} {
		if strings.Contains(strings.ToLower(s), filter) {
			return true
		}
	}
	return false
}

func (m model) getFilteredDatabases() []pgui.Database {
	o := m.state.databaseOverview
	return filterBy(o.databases, o.filterText, databaseMatches)
}

func (m model) getFilteredBackends() []pgui.Backend {
	o := m.state.databaseOverview
	return filterBy(o.backends, o.filterText, backendMatches)
}

func (m model) currentDatabase() (pgui.Database, bool) {
	dbs := m.getFilteredDatabases()
	if len(dbs) == 0 {
		return pgui.Database{}, false
	}
	return dbs[m.state.databaseOverview.selectedDatabase], true
}

func (m model) currentBackend() (pgui.Backend, bool) {
	bs := m.getFilteredBackends()
	if len(bs) == 0 {
		return pgui.Backend{}, false
	}
	return bs[m.state.databaseOverview.selectedBackend], true
}

func (m model) updateDatabasesTable() model {
	o := &m.state.databaseOverview
	var rows []tableRow
	for _, d := range m.getFilteredDatabases() {
		name := cell{styles.B(d.Name, styles.ToneText)}
		switch {
		case o.selectedDatabases[d.Name]:
			name = cell{styles.S("● ", styles.ToneAccent), styles.B(d.Name, styles.ToneText)}
		case d.Name == m.info.Database:
			name = cell{styles.S("› ", styles.ToneAccent), styles.B(d.Name, styles.ToneText)}
		}
		if d.Template {
			name = append(name, styles.S("  template", styles.ToneFaint))
		}
		size := text("-", styles.ToneFaint)
		if d.SizeKnown {
			size = text(formatBytes(d.Size), styles.ToneBody)
		}
		rows = append(rows, tableRow{
			name,
			text(d.Owner, styles.ToneBody),
			text(d.Encoding, styles.ToneMuted),
			text(d.Collation, styles.ToneMuted),
			size,
			tableCount(uint64(d.Connections), styles.ToneBody),
			text(orDash(d.Comment), styles.ToneFaint),
		})
	}
	o.databasesTable.empty = "loading databases…"
	if o.databasesLoaded {
		o.databasesTable.empty = "no databases match the filter"
	}
	o.selectedDatabase = setRows(&o.databasesTable, rows, o.selectedDatabase)
	return m
}

// backendState renders the state of a backend, lit while it works and warm while a transaction
// sits idle in it.
func backendState(b pgui.Backend) cell {
	switch {
	case len(b.BlockedBy) > 0:
		return cell{styles.S("● ", styles.ToneDanger), styles.S("blocked", styles.ToneDanger)}
	case b.State == "active":
		return cell{styles.S("● ", styles.ToneSuccess), styles.S("active", styles.ToneBody)}
	case strings.HasPrefix(b.State, "idle in transaction"):
		return cell{styles.S("● ", styles.ToneWarning), styles.S(strings.TrimPrefix(b.State, "idle "), styles.ToneWarning)}
	case b.State == "":
		return text(strings.TrimSuffix(b.Type, " backend"), styles.ToneFaint)
	}
	return text(b.State, styles.ToneMuted)
}

// backendSince is when what the backend does began: its query while it runs one, its state
// otherwise.
func backendSince(b pgui.Backend) time.Time {
	if b.State == "active" {
		return b.QueryStart
	}
	return b.StateChange
}

// formatFor renders how long something has been going on, e.g. 3s, 4m12s or 2h5m.
func formatFor(since time.Time) string {
	if since.IsZero() {
		return "-"
	}
	d := time.Since(since)
	switch {
	case d < time.Second:
		return "<1s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return formatUptime(d)
}

// oneLine puts a query on one line, its whitespace collapsed.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (m model) updateBackendsTable() model {
	o := &m.state.databaseOverview
	var rows []tableRow
	for _, b := range m.getFilteredBackends() {
		pid := text(strconv.Itoa(b.PID), styles.ToneText)
		if o.selectedBackends[b.PID] {
			pid = cell{styles.S("● ", styles.ToneAccent), styles.S(strconv.Itoa(b.PID), styles.ToneText)}
		}
		app := text(orDash(b.Application), styles.ToneBody)
		if b.Application == m.appName() {
			app = text("pgui", styles.ToneFaint)
		}
		wait := text("-", styles.ToneFaint)
		switch {
		case len(b.BlockedBy) > 0:
			wait = text("pid "+pidJoin(b.BlockedBy), styles.ToneDanger)
		case b.WaitEvent != "" && b.State == "active":
			wait = cell{styles.S(b.WaitEventType+" ", styles.ToneFaint), styles.S(b.WaitEvent, styles.ToneBody)}
		}
		since := backendSince(b)
		age := text(formatFor(since), styles.ToneMuted)
		if b.State == "active" && !since.IsZero() && time.Since(since) > time.Minute {
			age = text(formatFor(since), styles.ToneWarning)
		}
		query := text(orDash(oneLine(b.Query)), styles.ToneBody)
		if b.State != "active" {
			query = text(orDash(oneLine(b.Query)), styles.ToneFaint)
		}
		rows = append(rows, tableRow{
			pid,
			text(orDash(b.User), styles.ToneBody),
			text(orDash(b.Database), styles.ToneBody),
			app,
			text(orDash(b.Client), styles.ToneMuted),
			backendState(b),
			wait,
			age,
			query,
		})
	}
	o.backendsTable.empty = "loading activity…"
	if o.backendsLoaded {
		o.backendsTable.empty = "no backends match the filter"
	}
	o.selectedBackend = setRows(&o.backendsTable, rows, o.selectedBackend)
	return m
}

func pidJoin(pids []int) string {
	s := make([]string, len(pids))
	for i, p := range pids {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}

// pidList names the backends a dialog or status is about.
func pidList(pids []int) string {
	if len(pids) == 1 {
		return fmt.Sprintf("pid %d", pids[0])
	}
	return plural(len(pids), "backend")
}

func (m model) applyDatabases(msg messages.DatabasesLoadedMsg) (model, tea.Cmd) {
	o := &m.state.databaseOverview
	onPage := m.page == databaseOverview && o.tab == tabDatabases
	if onPage {
		m.loading = false
	}
	var cmds []tea.Cmd
	if msg.Err != nil {
		if onPage {
			var cmd tea.Cmd
			m, cmd = m.loadError("could not load databases", msg.Err, o.databasesLoaded)
			cmds = append(cmds, cmd)
		}
	} else {
		prev, ok := m.currentDatabase()
		o.databases = msg.Databases
		o.databasesLoaded = true
		if onPage {
			m.refreshedAt = time.Now()
		}
		prune(o.selectedDatabases, o.databases, func(d pgui.Database) string { return d.Name })
		switch {
		case o.cursorTo != "":
			o.selectedDatabase = follow(m.getFilteredDatabases(), o.selectedDatabase, func(d pgui.Database) bool { return d.Name == o.cursorTo })
			o.cursorTo = ""
		case ok:
			o.selectedDatabase = follow(m.getFilteredDatabases(), o.selectedDatabase, func(d pgui.Database) bool { return d.Name == prev.Name })
		}
		m = m.updateDatabasesTable()
	}
	if onPage {
		var cmd tea.Cmd
		m, cmd = m.scheduleRefresh()
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) applyBackends(msg messages.BackendsLoadedMsg) (model, tea.Cmd) {
	o := &m.state.databaseOverview
	onTab := m.page == databaseOverview && o.tab == tabActivity
	onDetails := m.page == backendDetails
	if onTab {
		m.loading = false
	}
	var cmds []tea.Cmd
	if msg.Err != nil {
		if onTab || onDetails {
			var cmd tea.Cmd
			m, cmd = m.loadError("could not load activity", msg.Err, o.backendsLoaded)
			cmds = append(cmds, cmd)
		}
	} else {
		prev, ok := m.currentBackend()
		o.backends = msg.Backends
		o.backendsLoaded = true
		if onTab || onDetails {
			m.refreshedAt = time.Now()
		}
		prune(o.selectedBackends, o.backends, func(b pgui.Backend) int { return b.PID })
		if ok {
			o.selectedBackend = follow(m.getFilteredBackends(), o.selectedBackend, func(b pgui.Backend) bool { return b.PID == prev.PID })
		}
		m = m.updateBackendsTable()
		if onDetails {
			m = m.syncBackendDetails()
		}
	}
	if onTab || onDetails {
		var cmd tea.Cmd
		m, cmd = m.scheduleRefresh()
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// loadOverviewTab loads what the open tab of the overview lists.
func (m model) loadOverviewTab() tea.Cmd {
	if m.state.databaseOverview.tab == tabActivity {
		return commands.LoadBackends(m.context, m.pool)
	}
	return commands.LoadDatabases(m.context, m.pool)
}

// DatabaseOverviewGoBack returns to the overview as it was left and refreshes it in the background.
func (m model) DatabaseOverviewGoBack() (model, tea.Cmd) {
	m.error = ""
	m = m.SwitchPage(databaseOverview)
	return m, m.loadOverviewTab()
}

func (m model) focusOverviewTab() model {
	o := &m.state.databaseOverview
	o.databasesTable.Blur()
	o.backendsTable.Blur()
	o.activeTable().Focus()
	return m
}

// switchOverviewTab opens the next or previous tab, loading it when it was never shown.
func (m model) switchOverviewTab(delta int) (model, tea.Cmd) {
	o := &m.state.databaseOverview
	o.tab = overviewTab(nextTab(int(o.tab), delta, int(overviewTabCount)))
	m.refreshedAt = time.Time{}
	m = m.focusOverviewTab().updateDatabasesTable().updateBackendsTable()
	return m, m.loadOverviewTab()
}

// clearFilterOrSelection clears the filter, or else the selection of the open tab, and reports
// whether there was either to clear.
func (m model) clearOverviewFilterOrSelection() (model, bool) {
	o := &m.state.databaseOverview
	switch {
	case o.filterText != "":
		o.filterText = ""
		o.filterInput.SetValue("")
		o.selectedDatabase, o.selectedBackend = 0, 0
	case o.tab == tabDatabases && len(o.selectedDatabases) > 0:
		o.selectedDatabases = make(map[string]bool)
	case o.tab == tabActivity && len(o.selectedBackends) > 0:
		o.selectedBackends = make(map[int]bool)
	default:
		return m, false
	}
	return m.updateDatabasesTable().updateBackendsTable(), true
}

// openDatabase shows the tables of a database, connecting to it first when it is not the one
// the pool is on.
func (m model) openDatabase(name string) (model, tea.Cmd) {
	if name == m.info.Database {
		m.state.databaseDetails.database = name
		return m.DatabaseDetailsSwitchPage()
	}
	if m, refused := m.refuseOffline("opened"); refused {
		return m, nil
	}
	m.loading, m.loadingMsg = true, "connecting to "+name+"…"
	return m, commands.OpenDatabase(m.context, client.WithDatabase(m.config, name))
}

func (m model) openedDatabase(msg messages.DatabaseOpenedMsg) (model, tea.Cmd) {
	m.loading = false
	if msg.Err != nil {
		m.error = fmt.Sprintf("could not connect to %s: %s", msg.Name, errorText(msg.Err))
		return m, nil
	}
	m, swap := m.replacePool(msg.Pool, msg.Config)
	m.state.databaseDetails.database = msg.Name
	m = m.updateDatabasesTable()
	m, cmd := m.DatabaseDetailsSwitchPage()
	return m, tea.Batch(swap, cmd)
}

func (m model) DatabaseOverviewUpdate(msg tea.Msg) (model, tea.Cmd) {
	var cmd tea.Cmd
	o := &m.state.databaseOverview

	if o.filtering {
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(msg, m.keys.Back):
				o.filtering = false
				o.filterText = ""
				o.filterInput.SetValue("")
				o.filterInput.Blur()
				return m.updateDatabasesTable().updateBackendsTable(), nil
			case key.Matches(msg, m.keys.View):
				o.filtering = false
				o.filterInput.Blur()
				return m, nil
			}
		}
		o.filterInput, cmd = o.filterInput.Update(msg)
		if o.filterText != o.filterInput.Value() {
			o.filterText = o.filterInput.Value()
			o.selectedDatabase, o.selectedBackend = 0, 0
		}
		return m.updateDatabasesTable().updateBackendsTable(), cmd
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	switch {
	case key.Matches(keyMsg, m.keys.SwitchTab):
		return m.switchOverviewTab(1)
	case key.Matches(keyMsg, m.keys.PrevTab):
		return m.switchOverviewTab(-1)
	case key.Matches(keyMsg, m.keys.Filter):
		o.filtering = true
		o.filterInput.Placeholder = map[overviewTab]string{
			tabDatabases: "filter by name, owner or comment…",
			tabActivity:  "filter by pid, user, database, application, state or query…",
		}[o.tab]
		return m, o.filterInput.Focus()
	case key.Matches(keyMsg, m.keys.Refresh):
		if m.disconnected() {
			return m.setStatus(offlineStatus, styles.ToneWarning)
		}
		return m, m.loadOverviewTab()
	case key.Matches(keyMsg, m.keys.Editor):
		return m.SQLEditorSwitchPage("")
	case key.Matches(keyMsg, m.keys.Back):
		m, _ = m.clearOverviewFilterOrSelection()
		return m, nil
	case key.Matches(keyMsg, m.keys.Quit):
		if m, ok := m.clearOverviewFilterOrSelection(); ok {
			return m, nil
		}
		return m, tea.Quit
	}

	if o.tab == tabActivity {
		return m.activityKey(keyMsg)
	}
	return m.databasesKey(keyMsg)
}

func (m model) databasesKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	o := &m.state.databaseOverview
	switch {
	case key.Matches(msg, m.keys.View):
		if d, ok := m.currentDatabase(); ok {
			if !d.AllowConn {
				return m.setStatus(d.Name+" does not allow connections", styles.ToneWarning)
			}
			return m.openDatabase(d.Name)
		}
	case key.Matches(msg, m.keys.Select):
		if d, ok := m.currentDatabase(); ok {
			toggle(o.selectedDatabases, d.Name)
		}
		return m.updateDatabasesTable(), nil
	case key.Matches(msg, m.keys.CopyToClipboard):
		if d, ok := m.currentDatabase(); ok {
			return m, commands.CopyToClipboard(d.Name)
		}
	case key.Matches(msg, m.keys.Create):
		if m, cmd, refused := m.refuseReadOnly(); refused {
			return m, cmd
		}
		return m.DatabaseCreateSwitchPage()
	case key.Matches(msg, m.keys.Delete):
		if m, cmd, refused := m.refuseReadOnly(); refused {
			return m, cmd
		}
		names, hidden := selectedWithHidden(o.selectedDatabases, m.getFilteredDatabases(), func(d pgui.Database) string { return d.Name }, m.currentDatabase)
		if len(names) == 0 {
			if hidden > 0 {
				return m.setStatus("the selected databases are hidden by the filter", styles.ToneWarning)
			}
			return m, nil
		}
		if slices.Contains(names, m.info.Database) {
			return m.setStatus("connected to "+m.info.Database+", open another database to drop it", styles.ToneWarning)
		}
		m.state.databaseDrop = databaseDropState{names: names, hidden: hidden}
		return m.DatabaseDropSwitchPage()
	default:
		o.databasesTable = o.databasesTable.Update(msg)
		o.selectedDatabase = o.databasesTable.Cursor()
	}
	return m, nil
}

func (m model) activityKey(msg tea.KeyPressMsg) (model, tea.Cmd) {
	o := &m.state.databaseOverview
	switch {
	case key.Matches(msg, m.keys.View):
		if b, ok := m.currentBackend(); ok {
			return m.BackendDetailsSwitchPage(b)
		}
	case key.Matches(msg, m.keys.Select):
		if b, ok := m.currentBackend(); ok {
			toggle(o.selectedBackends, b.PID)
		}
		return m.updateBackendsTable(), nil
	case key.Matches(msg, m.keys.CopyToClipboard):
		if b, ok := m.currentBackend(); ok && b.Query != "" {
			return m, commands.CopyToClipboard(b.Query)
		}
	case key.Matches(msg, m.keys.CancelQuery), key.Matches(msg, m.keys.Delete):
		pids, hidden := selectedWithHidden(o.selectedBackends, m.getFilteredBackends(), func(b pgui.Backend) int { return b.PID }, m.currentBackend)
		if len(pids) == 0 {
			if hidden > 0 {
				return m.setStatus("the selected backends are hidden by the filter", styles.ToneWarning)
			}
			return m, nil
		}
		return m.BackendSignalSwitchPage(pids, hidden, key.Matches(msg, m.keys.Delete), false)
	default:
		o.backendsTable = o.backendsTable.Update(msg)
		o.selectedBackend = o.backendsTable.Cursor()
	}
	return m, nil
}

// selectedWithHidden returns the selected items the filter shows, or the item under the cursor
// when nothing is selected. hidden counts the selected items the filter hides, which an action on
// the selection leaves alone.
func selectedWithHidden[K comparable, T any](selected map[K]bool, items []T, keyOf func(T) K, current func() (T, bool)) ([]K, int) {
	keys := selectedOr(selected, items, keyOf, current)
	hidden := 0
	if len(selected) > 0 {
		hidden = len(selected) - len(keys)
	}
	return keys, hidden
}

func (m model) DatabaseOverviewView() string {
	o := &m.state.databaseOverview
	tabs := renderTabs([]tabItem{
		{name: "databases", count: len(o.databasesTable.rows), loaded: o.databasesLoaded},
		{name: "activity", count: len(o.backendsTable.rows), loaded: o.backendsLoaded},
	}, int(o.tab), m.overviewHint()...)
	return tabs + "\n\n" + lipgloss.PlaceHorizontal(contentWidth-4, lipgloss.Left, o.activeTable().View())
}

// overviewHint says what the open tab is about, in a long and a short wording.
func (m model) overviewHint() []string {
	if m.state.databaseOverview.tab == tabActivity {
		return []string{"client backends first, the longest running at the top", "longest running first"}
	}
	return []string{"› marks the database pgui is connected to", "› connected"}
}
