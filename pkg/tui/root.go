package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kontrolplane/tablespace/pkg/client"
	keys "github.com/kontrolplane/tablespace/pkg/keys"
	"github.com/kontrolplane/tablespace/pkg/tablespace"
	"github.com/kontrolplane/tablespace/pkg/tui/commands"
	"github.com/kontrolplane/tablespace/pkg/tui/messages"
	"github.com/kontrolplane/tablespace/pkg/tui/styles"
)

func NewModel(
	projectName string,
	programName string,
	pool *pgxpool.Pool,
	config *pgxpool.Config,
	info client.Info,
) tea.Model {
	m := newModel(projectName, programName)
	m.pool = pool
	m.config = config
	m.info = info
	m.session = tablespace.NewSession(config.ConnConfig)
	return m
}

func newModel(projectName, programName string) model {
	m := model{
		projectName: projectName,
		programName: programName,
		page:        databaseOverview,
		context:     context.Background(),
		online:      true,
		loading:     true,
		loadingMsg:  "loading databases…",
		spinner:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		keys:        keys.Keys,
	}
	m.state = state{
		databaseOverview: databaseOverviewState{
			databasesTable:    newDataTable(databaseColumns, contentWidth-4, m.overviewTableHeight()),
			backendsTable:     newDataTable(backendColumns, contentWidth-4, m.overviewTableHeight()),
			selectedDatabases: make(map[string]bool),
			selectedBackends:  make(map[int]bool),
			filterInput:       initFilterInput("filter by name, owner or query…"),
		},
		databaseDetails: databaseDetailsState{
			filterInput: initFilterInput("filter by schema, name or definition…"),
		},
		tableDetails: tableDetailsState{
			selectedRows: make(map[string]bool),
			filterInput:  initFilterInput("search the loaded rows…"),
			whereInput:   initWhereInput(),
			follow:       true,
		},
		sqlEditor: newSQLEditorState(),
	}
	m.state.backendDetails.view = newScroller()
	return m
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{commands.LoadDatabases(m.context, m.pool), m.spinner.Tick}
	if m.pool != nil {
		cmds = append(cmds, commands.LoadServer(m.context, m.pool))
	}
	return tea.Batch(cmds...)
}

// textInputActive reports whether keystrokes are currently going into a text field,
// in which case single character shortcuts must not trigger.
func (m model) textInputActive() bool {
	switch m.page {
	case databaseCreate, databaseDrop, tableDrop:
		return true
	case databaseOverview:
		return m.state.databaseOverview.filtering
	case databaseDetails:
		return m.state.databaseDetails.filtering
	case tableDetails:
		td := m.state.tableDetails
		return td.filtering || td.whereEditing
	case sqlEditor:
		e := m.state.sqlEditor
		return e.focus == editorFocusInput || e.historyOpen
	}
	return false
}

// disconnected reports whether the server did not answer the last round trip, which the frame
// already shows.
func (m model) disconnected() bool {
	return m.pool != nil && !m.online
}

// refuseOffline answers an action that would change something while the server is not reachable.
// It is refused rather than queued, so nothing is done twice once the connection is back.
func (m model) refuseOffline(nothing string) (model, bool) {
	if m.disconnected() {
		m.error = notConnected(nothing)
		return m, true
	}
	return m, false
}

// refuseReadOnly answers an action that would change something on a read only connection.
func (m model) refuseReadOnly() (model, tea.Cmd, bool) {
	if m.info.ReadOnly {
		m, cmd := m.setStatus("connected read only, nothing can be changed", styles.ToneWarning)
		return m, cmd, true
	}
	return m, nil, false
}

// keepSpinning restarts the loading spinner when a load began after it last stopped.
func (m model) keepSpinning() (model, tea.Cmd) {
	if (!m.loading && !m.state.sqlEditor.running) || m.spinning {
		return m, nil
	}
	m.spinning = true
	return m, m.spinner.Tick
}

func (m model) scheduleRefresh() (model, tea.Cmd) {
	m.refreshGen++
	return m, commands.ScheduleRefresh(m.refreshGen)
}

// setStatus shows text in the footer for a few seconds, in the success, warning or danger tone.
func (m model) setStatus(text string, tone styles.Tone) (model, tea.Cmd) {
	m.statusGen++
	m.statusMsg = text
	m.statusTone = tone
	return m, commands.ClearStatusAfter(3*time.Second, m.statusGen)
}

// withStatus is setStatus for a handler that already has a command to return.
func (m model) withStatus(cmd tea.Cmd, text string, tone styles.Tone) (model, tea.Cmd) {
	m, status := m.setStatus(text, tone)
	return m, tea.Batch(cmd, status)
}

// finish ends a create, drop, delete or signal once its result is in: it stops the spinner,
// navigates with back, and reports done in the footer, or failed with the error in a dialog.
func (m model) finish(back func(model) (model, tea.Cmd), done, failed string, err error) (model, tea.Cmd) {
	m.busy, m.loading = false, false
	m, cmd := back(m)
	if err != nil {
		m.error = fmt.Sprintf("%s: %s", failed, errorText(err))
		return m, cmd
	}
	return m.withStatus(cmd, done, styles.ToneSuccess)
}

// bulkResult describes the outcome of acting on items: what succeeded, and what to say when some
// or all of them failed.
func bulkResult(verb, past, noun string, items, done []string) (string, string) {
	label := func(names []string) string {
		if len(names) == 1 {
			return noun + " " + names[0]
		}
		return plural(len(names), noun)
	}
	ok := past + " " + label(done)
	if len(done) == 0 {
		return ok, "could not " + verb + " " + label(items)
	}
	return ok, fmt.Sprintf("%s %d of %s, the others failed", past, len(done), plural(len(items), noun))
}

// loadError reports a failed load. Data already on screen stays there with the error in the
// footer, so a refresh failing every few seconds does not keep raising a dialog. While the
// connection is down the frame says so and the error is not repeated.
func (m model) loadError(what string, err error, onScreen bool) (model, tea.Cmd) {
	switch {
	case m.disconnected():
		return m, nil
	case onScreen:
		return m.setStatus(fmt.Sprintf("%s: %s", what, errorText(err)), styles.ToneDanger)
	}
	m.error = fmt.Sprintf("%s: %s", what, errorText(err))
	return m, nil
}

// hasControlChars reports whether s holds control characters other than line breaks and tabs.
func hasControlChars(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r'
	})
}

// errorText words err for the interface. A server error keeps its message with the detail and
// hint that come with it, without the severity and code.
func errorText(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		text := pgErr.Message
		if pgErr.Detail != "" {
			text += "\n" + pgErr.Detail
		}
		if pgErr.Hint != "" {
			text += "\nhint: " + pgErr.Hint
		}
		return text
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "the server did not answer in time"
	}
	return client.Redact(err.Error())
}

// offlineStatus answers a refresh asked for while the connection is down.
const offlineStatus = "not connected, showing cached data"

// notConnected is what an action refused while the connection is down says.
func notConnected(nothing string) string {
	return "not connected to postgres, nothing was " + nothing
}

// replacePool switches to a pool connected to another database. The old pool and the editor's
// session on the old database are closed.
func (m model) replacePool(pool *pgxpool.Pool, cfg *pgxpool.Config) (model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.pool != nil && m.pool != pool {
		cmds = append(cmds, commands.ClosePool(m.pool))
	}
	if m.session != nil {
		cmds = append(cmds, commands.CloseSession(m.session))
	}
	m.pool, m.config = pool, cfg
	m.info = client.InfoOf(cfg, m.info.ReadOnly)
	m.session = tablespace.NewSession(cfg.ConnConfig)
	m.serverOK, m.online = false, true
	m.state.sqlEditor.result = nil
	cmds = append(cmds, commands.LoadServer(m.context, pool))
	return m, tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		setLayout(msg.Width, msg.Height)
		m = m.resize()

	case spinner.TickMsg:
		if !m.loading && !m.state.sqlEditor.running {
			m.spinning = false
			return m, nil
		}
		m.spinning = true
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case messages.ServerLoadedMsg:
		if msg.Err == nil {
			m.server = msg.Server
			m.serverOK = true
			m.online = true
		} else {
			m.online = false
		}
		return m, commands.ScheduleServerRefresh()

	case messages.ServerTickMsg:
		return m, commands.LoadServer(m.context, m.pool)

	case tea.KeyPressMsg:
		if key.Matches(msg, m.keys.ForceQuit) {
			return m, tea.Quit
		}
		if m.busy || m.tooSmall() {
			return m, nil
		}
		if m.error != "" {
			m.error = ""
			return m, nil
		}
		if !m.textInputActive() && key.Matches(msg, m.keys.Help) {
			m.showHelp = !m.showHelp
			return m, nil
		}
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		if !key.Matches(msg, m.keys.Back) {
			m.armed = false
		}
		if !m.textInputActive() && m.refreshes() && key.Matches(msg, m.keys.Pause) {
			return m.togglePause()
		}

	case messages.ClockTickMsg:
		m.clocking = false
		return m.keepClock()

	case messages.DatabasesLoadedMsg:
		m, cmd = m.applyDatabases(msg)
		cmds = append(cmds, cmd)

	case messages.BackendsLoadedMsg:
		m, cmd = m.applyBackends(msg)
		cmds = append(cmds, cmd)

	case messages.DatabaseOpenedMsg:
		return m.openedDatabase(msg)

	case messages.TablesLoadedMsg:
		m, cmd = m.applyTables(msg)
		cmds = append(cmds, cmd)

	case messages.IndexesLoadedMsg:
		m, cmd = m.applyIndexes(msg)
		cmds = append(cmds, cmd)

	case messages.TableLoadedMsg:
		m, cmd = m.applyTable(msg)
		cmds = append(cmds, cmd)

	case messages.SchemaLoadedMsg:
		m, cmd = m.applySchema(msg)
		cmds = append(cmds, cmd)

	case messages.RowsLoadedMsg:
		m, cmd = m.applyRows(msg)
		cmds = append(cmds, cmd)

	case messages.RowsDeletedMsg:
		td := &m.state.tableDetails
		td.selectedRows = make(map[string]bool)
		done := fmt.Sprintf("deleted %s from %s", plural(int(msg.Deleted), "row"), td.table.DisplayName())
		failed := "could not delete " + plural(len(m.state.rowDelete.keys), "row")
		if msg.Err == nil && int(msg.Deleted) < len(m.state.rowDelete.keys) {
			m.busy, m.loading = false, false
			m, cmd = m.TableDetailsGoBack()
			return m.withStatus(cmd, done+", the others were already gone", styles.ToneWarning)
		}
		return m.finish(model.TableDetailsGoBack, done, failed, msg.Err)

	case messages.DatabaseCreatedMsg:
		m.busy, m.loading = false, false
		if msg.Err != nil {
			return m.reopenDatabaseCreate("could not create database: " + errorText(msg.Err))
		}
		m.state.databaseOverview.cursorTo = msg.Name
		m, cmd = m.DatabaseOverviewGoBack()
		return m.withStatus(cmd, "created database "+msg.Name, styles.ToneSuccess)

	case messages.DatabasesDroppedMsg:
		o := &m.state.databaseOverview
		for _, name := range msg.Names {
			delete(o.selectedDatabases, name)
		}
		o.databases = deleteNames(o.databases, msg.Names, func(d tablespace.Database) string { return d.Name })
		m = m.updateDatabasesTable()
		done, failed := bulkResult("drop", "dropped", "database", m.state.databaseDrop.names, msg.Names)
		return m.finish(model.DatabaseOverviewGoBack, done, failed, msg.Err)

	case messages.BackendsSignalledMsg:
		o := &m.state.databaseOverview
		for _, pid := range msg.PIDs {
			delete(o.selectedBackends, pid)
		}
		verb := "cancelled the query of"
		if msg.Terminate {
			verb = "terminated"
		}
		s := m.state.backendSignal
		done := fmt.Sprintf("%s %s", verb, pidList(msg.PIDs))
		failed := "could not cancel the query of " + pidList(s.pids)
		if s.terminate {
			failed = "could not terminate " + pidList(s.pids)
		}
		if len(msg.PIDs) > 0 && msg.Err != nil {
			failed = fmt.Sprintf("%s %d of %s, the others failed", verb, len(msg.PIDs), plural(len(s.pids), "backend"))
		}
		return m.finish(model.backendSignalGoBack, done, failed, msg.Err)

	case messages.TableTruncatedMsg:
		td := &m.state.tableDetails
		if msg.Table.OID == td.table.OID {
			td.selectedRows = make(map[string]bool)
			m = m.resetRows()
		}
		name := msg.Table.Kind + " " + msg.Table.DisplayName()
		return m.finish(model.TableDetailsGoBack, "truncated "+name, "could not truncate "+name, msg.Err)

	case messages.TableDroppedMsg:
		name := msg.Table.Kind + " " + msg.Table.DisplayName()
		if msg.Err != nil {
			return m.finish(model.TableDetailsGoBack, "", "could not drop "+name, msg.Err)
		}
		d := &m.state.databaseDetails
		d.tables = deleteNames(d.tables, []uint32{msg.Table.OID}, func(t tablespace.Table) uint32 { return t.OID })
		m = m.updateTablesTable()
		return m.finish(model.DatabaseDetailsGoBack, "dropped "+name, "", nil)

	case messages.QueryRanMsg:
		m, cmd = m.applyQuery(msg)
		cmds = append(cmds, cmd)

	case messages.ClipboardCopiedMsg:
		if msg.Err != nil {
			cmds = append(cmds, tea.SetClipboard(msg.Text))
		}
		if hasControlChars(msg.Text) {
			m, cmd = m.setStatus("copied, the value contains control characters", styles.ToneWarning)
		} else {
			m, cmd = m.setStatus("copied to clipboard", styles.ToneSuccess)
		}
		cmds = append(cmds, cmd)

	case messages.StatusClearMsg:
		if msg.Gen == m.statusGen {
			m.statusMsg = ""
		}

	case messages.RefreshTickMsg:
		// While paused the tick is let go, which ends the refresh loop until it resumes.
		if msg.Gen != m.refreshGen || m.paused {
			break
		}
		m, cmd = m.refreshPage()
		cmds = append(cmds, cmd)
	}

	switch m.page {
	case databaseOverview:
		m, cmd = m.DatabaseOverviewUpdate(msg)
	case databaseCreate:
		m, cmd = m.DatabaseCreateUpdate(msg)
	case databaseDrop:
		m, cmd = m.DatabaseDropUpdate(msg)
	case backendDetails:
		m, cmd = m.BackendDetailsUpdate(msg)
	case backendSignal:
		m, cmd = m.BackendSignalUpdate(msg)
	case databaseDetails:
		m, cmd = m.DatabaseDetailsUpdate(msg)
	case tableDetails:
		m, cmd = m.TableDetailsUpdate(msg)
	case tableDrop:
		m, cmd = m.TableDropUpdate(msg)
	case rowDetails:
		m, cmd = m.RowDetailsUpdate(msg)
	case rowDelete:
		m, cmd = m.RowDeleteUpdate(msg)
	case sqlEditor:
		m, cmd = m.SQLEditorUpdate(msg)
	}
	cmds = append(cmds, cmd)
	m, cmd = m.keepSpinning()
	cmds = append(cmds, cmd)
	m, cmd = m.keepClock()
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = m.programName
	v.ForegroundColor = styles.P.Text
	if styles.Paint {
		v.BackgroundColor = styles.P.Base
	}
	return v
}

// tooSmall reports whether the terminal is smaller than the layout needs.
func (m model) tooSmall() bool {
	return m.width > 0 && (m.width < minContentWidth+chromeWidth || m.height < minContentHeight+chromeHeight)
}

// tooSmallView asks for a larger terminal, in place of a layout that would not fit.
func (m model) tooSmallView() string {
	notice := lipgloss.JoinVertical(lipgloss.Center,
		styles.Render(styles.B("terminal too small", styles.ToneWarning)),
		styles.Muted(fmt.Sprintf("%d×%d, needs %d×%d", m.width, m.height, minContentWidth+chromeWidth, minContentHeight+chromeHeight)),
		styles.Faint("ctrl+c to quit"),
	)
	return clip(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, notice), m.width, m.height)
}

func (m model) render() string {
	if m.tooSmall() {
		return m.tooSmallView()
	}

	c := m.content()
	meta, foot := m.frameMeta()
	mainView := m.renderHeader() + "\n\n" +
		frame(styles.Render(m.breadcrumb()...), meta, foot, c) + "\n" +
		m.renderFooter()

	if m.width == 0 {
		return mainView
	}
	return place(m.width, m.height, mainView)
}

// content renders what the frame holds: the page, with the help or an error over it, or the
// loading notice in its place.
func (m model) content() string {
	page := m.pageView()
	switch {
	case m.showHelp:
		return overlay(page, m.renderHelp())
	case m.error != "":
		return overlay(page, m.ErrorView())
	}
	return page
}

// pageView renders the page, a confirmation over the page it was asked from.
func (m model) pageView() string {
	if m.loading {
		return m.LoadingView()
	}
	if from, ok := m.dialogFrom(); ok {
		return overlay(m.view(from), m.view(m.page))
	}
	return m.view(m.page)
}

// dialogFrom is the page a confirmation was asked from, which stays in sight behind it.
func (m model) dialogFrom() (page, bool) {
	switch m.page {
	case databaseDrop:
		return databaseOverview, true
	case backendSignal:
		if m.state.backendSignal.fromDetails {
			return backendDetails, true
		}
		return databaseOverview, true
	case tableDrop:
		return tableDetails, true
	case rowDelete:
		if m.state.rowDelete.fromDetails {
			return rowDetails, true
		}
		return tableDetails, true
	}
	return 0, false
}

func (m model) view(p page) string {
	switch p {
	case databaseOverview:
		return m.DatabaseOverviewView()
	case databaseCreate:
		return m.DatabaseCreateView()
	case databaseDrop:
		return m.DatabaseDropView()
	case backendDetails:
		return m.BackendDetailsView()
	case backendSignal:
		return m.BackendSignalView()
	case databaseDetails:
		return m.DatabaseDetailsView()
	case tableDetails:
		return m.TableDetailsView()
	case tableDrop:
		return m.TableDropView()
	case rowDetails:
		return m.RowDetailsView()
	case rowDelete:
		return m.RowDeleteView()
	case sqlEditor:
		return m.SQLEditorView()
	}
	return errNoPageSelected
}

func (m model) LoadingView() string {
	msg := m.loadingMsg
	if msg == "" {
		msg = "loading…"
	}
	return lipgloss.Place(contentWidth, contentHeight, lipgloss.Center, lipgloss.Center,
		styles.Accent(m.spinner.View())+" "+styles.Muted(truncate(styles.Clean(msg), contentWidth-4)))
}

// refreshes reports whether the current page refreshes on its own.
func (m model) refreshes() bool {
	switch m.page {
	case databaseOverview, databaseDetails, tableDetails, backendDetails:
		return true
	}
	return false
}

// refreshPage reloads what the current page shows.
func (m model) refreshPage() (model, tea.Cmd) {
	switch m.page {
	case databaseOverview:
		return m, m.loadOverviewTab()
	case backendDetails:
		return m, commands.LoadBackends(m.context, m.pool)
	case databaseDetails:
		return m, m.loadDetailsTab()
	case tableDetails:
		return m.refreshTableDetails()
	}
	return m, nil
}

// togglePause stops the refresh ticks, or starts them again with a refresh.
func (m model) togglePause() (model, tea.Cmd) {
	m.paused = !m.paused
	if m.paused {
		return m, nil
	}
	return m.refreshPage()
}

// clockVisible reports whether the time since the last refresh is on screen.
func (m model) clockVisible() bool {
	return !m.refreshedAt.IsZero() && m.refreshes() && !m.loading && m.error == "" && !m.tooSmall()
}

// keepClock ticks the time since the last refresh while it is on screen, once each time its
// wording changes.
func (m model) keepClock() (model, tea.Cmd) {
	if m.clocking || !m.clockVisible() {
		return m, nil
	}
	m.clocking = true
	return m, commands.ScheduleClock(untilAgoChanges(time.Since(m.refreshedAt)))
}

// untilAgoChanges returns how long until formatAgo words an age of d differently.
func untilAgoChanges(d time.Duration) time.Duration {
	unit := time.Second
	switch {
	case d >= 24*time.Hour:
		unit = 24 * time.Hour
	case d >= time.Hour:
		unit = time.Hour
	case d >= time.Minute:
		unit = time.Minute
	}
	if d < 0 {
		return unit
	}
	return unit - d%unit
}

// formatRefreshed words the time since the last refresh.
func formatRefreshed(d time.Duration) string {
	if d < time.Second {
		return "refreshed just now"
	}
	return "refreshed " + formatAgo(time.Now().Add(-d))
}

// frameMeta returns what is set into the frame's edges: the active filter on top, and at the
// bottom the refresh clock, the cursor position of the visible table and the connection state.
func (m model) frameMeta() (string, string) {
	var filter, where string
	filterLabel := "filter "
	var t *dataTable
	var extra []string
	switch m.page {
	case databaseOverview:
		o := &m.state.databaseOverview
		filter = o.filterText
		t = o.activeTable()
	case databaseDetails:
		d := &m.state.databaseDetails
		filter = d.filterText
		t = d.activeTable()
	case tableDetails:
		td := &m.state.tableDetails
		filter = td.filterText
		t = td.activeTable()
		if td.tab == tabRows {
			where = td.where
			extra = m.rowsFootMeta()
			filterLabel = "search "
		}
	case rowDetails:
		t = &m.state.rowDetails.table
	case sqlEditor:
		if e := &m.state.sqlEditor; e.focus == editorFocusResults {
			t = &e.table
		}
	}
	var meta []string
	if where != "" {
		meta = append(meta, styles.Render(styles.S("where ", styles.ToneFaint), styles.S(strings.Join(strings.Fields(where), " "), styles.ToneText)))
	}
	if filter != "" {
		meta = append(meta, styles.Render(styles.S(filterLabel, styles.ToneFaint), styles.S(filter, styles.ToneText)))
	}
	metaText := strings.Join(meta, styles.Faint(" · "))
	if m.loading || m.error != "" {
		return metaText, m.connectionState()
	}
	foot := extra
	if m.clockVisible() && !m.disconnected() {
		foot = append(foot, styles.Faint(formatRefreshed(time.Since(m.refreshedAt))))
	}
	if t != nil && t.position() != "" {
		foot = append(foot, styles.Faint(t.position()))
	}
	foot = append(foot, m.connectionState())
	return metaText, strings.Join(foot, styles.Faint(" · "))
}

func (m model) renderFooter() string {
	var status string
	if m.statusMsg != "" {
		switch m.statusTone {
		case styles.ToneDanger:
			status = styles.Render(styles.S("✗ ", styles.ToneDanger), styles.S(m.statusMsg, styles.ToneDanger))
		case styles.ToneWarning:
			status = styles.Render(styles.S("▲ ", styles.ToneWarning), styles.S(m.statusMsg, styles.ToneWarning))
		default:
			status = styles.Render(styles.S("✓ ", styles.ToneSuccess), styles.S(m.statusMsg, styles.ToneBody))
		}
	}
	if w := lipgloss.Width(status); w > frameWidth/2 {
		status = ansi.Truncate(status, frameWidth/2, "…")
	}
	width := frameWidth - 2 - lipgloss.Width(status) - 2
	left := ansi.Truncate(m.renderFooterLeft(width), width, "…")
	return " " + spread(left, status+" ", frameWidth-1)
}

// renderFooterLeft renders what the footer shows next to the status, in at most width columns.
func (m model) renderFooterLeft(width int) string {
	switch m.page {
	case databaseOverview:
		o := m.state.databaseOverview
		switch {
		case o.filtering:
			return m.renderFilterBar(o.filterInput.View())
		case o.tab == tabDatabases && len(o.selectedDatabases) > 0:
			return m.renderSelectionInfo(len(o.selectedDatabases), "database", [2]string{"ctrl+d", "drop"})
		case o.tab == tabActivity && len(o.selectedBackends) > 0:
			return m.renderSelectionInfo(len(o.selectedBackends), "backend", [2]string{"ctrl+x", "cancel query"}, [2]string{"ctrl+d", "terminate"})
		}
	case databaseDetails:
		if d := m.state.databaseDetails; d.filtering {
			return m.renderFilterBar(d.filterInput.View())
		}
	case tableDetails:
		td := m.state.tableDetails
		switch {
		case td.whereEditing:
			return styles.Accent("where ") + td.whereInput.View() + "  " +
				hints([2]string{"enter", "apply"}, [2]string{"esc", "cancel"})
		case td.filtering:
			return m.renderFilterBar(td.filterInput.View())
		case td.tab == tabRows && len(td.selectedRows) > 0:
			return m.renderSelectionInfo(len(td.selectedRows), "row", [2]string{"ctrl+d", "delete"})
		}
	}
	return m.renderShortHelp(width)
}

func (m model) renderSelectionInfo(count int, itemType string, actions ...[2]string) string {
	pairs := append(slices.Clone(actions), [2]string{"q", "clear"})
	return styles.Render(styles.S("● ", styles.ToneAccent), styles.B(plural(count, itemType)+" selected", styles.ToneText)) +
		"    " + hints(pairs...)
}

func (m model) renderFilterBar(inputView string) string {
	return styles.Accent("/ ") + inputView + "  " +
		hints([2]string{"enter", "apply"}, [2]string{"esc", "clear"})
}

// fitHints renders the hints that fit width. Hints are dropped from the end but for the last two,
// help and back or quit, which stay.
func fitHints(width int, pairs ...[2]string) string {
	var key strings.Builder
	fmt.Fprint(&key, width)
	for _, p := range pairs {
		key.WriteString("\x00" + p[0] + "\x00" + p[1])
	}
	return hintsMemo.get(key.String(), func() string {
		pairs = slices.Clone(pairs)
		for len(pairs) > 2 && styledWidth(hints(pairs...)) > width {
			pairs = slices.Delete(pairs, len(pairs)-3, len(pairs)-2)
		}
		return hints(pairs...)
	})
}

var hintsMemo memo

func hints(pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = styles.Key(p[0], p[1])
	}
	return strings.Join(parts, styles.Faint("  ·  "))
}

var confirmHelp = [][2]string{{"y/n", "yes/no"}, {"←/→", "choose"}, {"enter", "confirm"}, {"esc", "cancel"}}

var shortHelp = map[page][][2]string{
	databaseCreate: {{"enter", "next"}, {"shift+tab", "previous"}, {"esc", "cancel"}},
	databaseDrop:   {{"enter", "drop"}, {"esc", "cancel"}},
	backendSignal:  confirmHelp,
	tableDrop:      {{"enter", "confirm"}, {"esc", "cancel"}},
	rowDelete:      confirmHelp,
}

// changes are the keys that change something, which a read only connection refuses.
var changes = []string{"ctrl+n", "ctrl+d", "ctrl+p", "ctrl+x"}

// pageHints lists the keys of a page in the same order on every page: what opens and selects,
// the tabs and the filter, what the page does on its own, what changes something, copying, the
// editor, pausing, and last help and back. A short footer drops them from the end, but for help and
// back, so the keys every page has stay in sight.
func pageHints(pairs ...[2]string) [][2]string {
	return slices.DeleteFunc(pairs, func(p [2]string) bool { return p[0] == "" })
}

// hint is a key with what it does, or nothing when on is false.
func hint(on bool, key, desc string) [2]string {
	if !on {
		return [2]string{}
	}
	return [2]string{key, desc}
}

func (m model) renderShortHelp(width int) string {
	return fitHints(width, m.shortHelp()...)
}

func (m model) shortHelp() [][2]string {
	switch {
	case m.busy:
		// Nothing cancels an action in flight, so no key is offered.
		return nil
	case m.error != "":
		return [][2]string{{"any key", "dismiss"}}
	}
	pairs := m.pageShortHelp()
	if m.info.ReadOnly {
		pairs = slices.DeleteFunc(slices.Clone(pairs), func(p [2]string) bool { return slices.Contains(changes, p[0]) })
	}
	return pairs
}

func (m model) pageShortHelp() [][2]string {
	switch m.page {
	case databaseOverview:
		if m.state.databaseOverview.tab == tabActivity {
			return pageHints(
				[2]string{"enter", "open"}, [2]string{"space", "select"},
				[2]string{"tab", "databases"}, [2]string{"/", "filter"},
				[2]string{"ctrl+x", "cancel query"}, [2]string{"ctrl+d", "terminate"},
				[2]string{"c", "copy query"}, [2]string{"e", "sql editor"}, [2]string{"p", "pause"},
				[2]string{"?", "help"}, [2]string{"q", "quit"})
		}
		return pageHints(
			[2]string{"enter", "open"}, [2]string{"space", "select"},
			[2]string{"tab", "activity"}, [2]string{"/", "filter"},
			[2]string{"ctrl+n", "new database"}, [2]string{"ctrl+d", "drop"},
			[2]string{"c", "copy name"}, [2]string{"e", "sql editor"}, [2]string{"p", "pause"},
			[2]string{"?", "help"}, [2]string{"q", "quit"})
	case backendDetails:
		return pageHints(
			[2]string{"↑/↓", "scroll"},
			[2]string{"ctrl+x", "cancel query"}, [2]string{"ctrl+d", "terminate"},
			[2]string{"c", "copy query"}, [2]string{"p", "pause"},
			[2]string{"?", "help"}, [2]string{"q", "back"})
	case databaseDetails:
		if m.state.databaseDetails.tab == tabDatabaseIndexes {
			return pageHints(
				[2]string{"enter", "open table"},
				[2]string{"tab", "tables"}, [2]string{"/", "filter"},
				[2]string{"c", "copy definition"}, [2]string{"e", "sql editor"}, [2]string{"p", "pause"},
				[2]string{"?", "help"}, [2]string{"q", "back"})
		}
		return pageHints(
			[2]string{"enter", "open"},
			[2]string{"tab", "indexes"}, [2]string{"/", "filter"},
			[2]string{"ctrl+n", "new table"},
			[2]string{"c", "copy name"}, [2]string{"e", "sql editor"}, [2]string{"p", "pause"},
			[2]string{"?", "help"}, [2]string{"q", "back"})
	case tableDetails:
		td := m.state.tableDetails
		next := tableTab(nextTab(int(td.tab), 1, int(tableTabCount))).String()
		if td.tab == tabRows {
			return pageHints(
				[2]string{"enter", "open"}, hint(td.paging.Deletable(), "space", "select"),
				[2]string{"tab", next}, [2]string{"/", "search"},
				[2]string{"w", "where"}, hint(!td.paging.ByOffset(), "f", "follow"), [2]string{"←/→", "columns"},
				hint(td.paging.Deletable(), "ctrl+d", "delete"),
				[2]string{"c", "copy row"}, [2]string{"e", "sql editor"}, [2]string{"p", "pause"},
				[2]string{"?", "help"}, [2]string{"q", "back"})
		}
		copied := map[tableTab]string{tabColumns: "copy name", tabIndexes: "copy definition", tabConstraints: "copy definition"}[td.tab]
		return pageHints(
			[2]string{"tab", next}, [2]string{"/", "filter"},
			hint(td.table.HoldsRows(), "ctrl+p", "truncate"), [2]string{"ctrl+d", "drop"},
			[2]string{"c", copied}, [2]string{"e", "sql editor"}, [2]string{"p", "pause"},
			[2]string{"?", "help"}, [2]string{"q", "back"})
	case rowDetails:
		d := m.state.rowDetails
		return pageHints(
			[2]string{"↑/↓", "field"}, [2]string{"pgup/pgdn", "scroll value"},
			hint(!d.fromQuery && m.state.tableDetails.paging.Deletable(), "ctrl+d", "delete"),
			[2]string{"c", "copy value"},
			[2]string{"?", "help"}, [2]string{"q", "back"})
	case sqlEditor:
		return m.editorHelp()
	}
	return shortHelp[m.page]
}

// renderHelp draws the key reference in a card, as roomy as the content area allows.
func (m model) renderHelp() string {
	var card string
	for _, fit := range []struct{ padY, padX, gap int }{{1, 4, 6}, {1, 2, 3}, {0, 2, 3}} {
		card = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.P.RuleBold).
			Padding(fit.padY, fit.padX).
			Render(m.renderHelpContent(contentWidth-4-2*fit.padX, contentHeight-2-2*fit.padY, fit.gap))
		if lipgloss.Width(card) <= contentWidth-2 && lipgloss.Height(card) <= contentHeight {
			break
		}
	}
	return card
}

// helpSection is a column of the key reference, for what the current page shows and the forms
// and dialogs opened from it.
type helpSection struct {
	id    string
	title string
	rows  [][2]string
}

var helpSections = []helpSection{
	{"databases", "databases", [][2]string{
		{"↑/k ↓/j", "move"},
		{"g / G", "first / last"},
		{"pgup/pgdn", "page"},
		{"enter", "open"},
		{"space", "select"},
		{"ctrl+n", "new database"},
		{"ctrl+d", "drop"},
		{"c", "copy name"},
		{"e", "sql editor"},
		{"tab", "activity"},
		{"/", "filter"},
		{"r / p", "refresh / pause"},
		{"q", "quit"},
	}},
	{"activity", "activity", [][2]string{
		{"enter", "open"},
		{"space", "select"},
		{"ctrl+x", "cancel query"},
		{"ctrl+d", "terminate"},
		{"c", "copy query"},
		{"tab", "databases"},
		{"/", "filter"},
		{"r / p", "refresh / pause"},
		{"q", "quit"},
	}},
	{"database", "database", [][2]string{
		{"enter", "open table"},
		{"ctrl+n", "new table"},
		{"c", "copy"},
		{"e", "sql editor"},
		{"tab", "tables / indexes"},
		{"/", "filter"},
		{"r / p", "refresh / pause"},
		{"q / esc", "back"},
	}},
	{"table", "table", [][2]string{
		{"enter", "open row"},
		{"space", "select"},
		{"↓ at end", "older rows"},
		{"←/→", "scroll columns"},
		{"w", "where clause"},
		{"f", "follow newest"},
		{"ctrl+d", "delete rows / drop"},
		{"ctrl+p", "truncate"},
		{"c", "copy"},
		{"e", "sql editor"},
		{"tab/⇧tab", "next / previous tab"},
		{"/", "search / filter"},
		{"r / p", "refresh / pause"},
		{"q / esc", "back"},
	}},
	{"row", "row", [][2]string{
		{"↑/↓", "field"},
		{"pgup/pgdn", "scroll value"},
		{"g / G", "first / last field"},
		{"ctrl+d", "delete"},
		{"c", "copy value"},
		{"q / esc", "back"},
	}},
	{"sql", "sql editor", [][2]string{
		{"ctrl+s", "run"},
		{"esc", "cancel a run"},
		{"ctrl+r", "history"},
		{"tab", "query / results"},
		{"enter", "open a result row"},
		{"c", "copy a result row"},
		{"q / esc", "back"},
	}},
}

// helpSection is the section the help puts first: the one of the current page, or for a form or
// dialog the page it was opened from.
func (m model) helpSection() string {
	p := m.page
	if from, ok := m.dialogFrom(); ok {
		p = from
	}
	switch p {
	case databaseOverview, databaseCreate:
		if p == databaseOverview && m.state.databaseOverview.tab == tabActivity {
			return "activity"
		}
		return "databases"
	case backendDetails:
		return "activity"
	case databaseDetails:
		return "database"
	case tableDetails:
		return "table"
	case rowDetails:
		if m.state.rowDetails.fromQuery {
			return "sql"
		}
		return "row"
	case sqlEditor:
		return "sql"
	}
	return ""
}

// renderHelpContent lays the sections out with the one of the current page first: side by side
// when they fit, else in rows of three or two, else the current page with the dialogs, else the
// current page alone.
func (m model) renderHelpContent(width, height, gap int) string {
	current := m.helpSection()
	title := func(s string, on bool) string {
		tone := styles.ToneMuted
		if on {
			tone = styles.ToneAccent
		}
		return styles.Fg(tone).Bold(true).MarginBottom(1).Render(s)
	}
	keyStyle := styles.Fg(styles.ToneText).Bold(true).Width(11)
	row := func(key, desc string) string {
		return keyStyle.Render(key) + styles.Muted(desc)
	}

	var columns []string
	for _, s := range helpSections {
		lines := []string{title(s.title, s.id == current)}
		for _, r := range s.rows {
			lines = append(lines, row(r[0], r[1]))
		}
		column := lipgloss.JoinVertical(lipgloss.Left, lines...)
		if s.id == current {
			columns = append([]string{column}, columns...)
		} else {
			columns = append(columns, column)
		}
	}

	dialogs := lipgloss.JoinVertical(lipgloss.Left,
		title("dialogs", false),
		row("y / n", "yes / no"),
		row("←/→ enter", "choose"),
		row("esc", "cancel"),
		row("ctrl+c", "quit"),
		"",
		styles.Muted("drops and truncates ask"),
		styles.Muted("to type the name first."),
	)
	columns = append(columns, dialogs)

	spaced := lipgloss.NewStyle().MarginRight(gap)
	grid := func(perRow int, cols ...string) string {
		var rows []string
		for i := 0; i < len(cols); i += perRow {
			line := slices.Clone(cols[i:min(i+perRow, len(cols))])
			for j := range line[:len(line)-1] {
				line[j] = spaced.Render(line[j])
			}
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, line...))
		}
		return lipgloss.JoinVertical(lipgloss.Left, rows...)
	}
	// Room for the closing hint under the sections.
	fits := func(s string) bool { return lipgloss.Width(s) <= width && lipgloss.Height(s) <= height-2 }

	body := columns[0]
	for _, layout := range []string{
		grid(len(columns), columns...),
		grid(3, columns...),
		grid(2, columns...),
		grid(2, columns[0], dialogs),
	} {
		if fits(layout) {
			body = layout
			break
		}
	}
	return lipgloss.JoinVertical(lipgloss.Center, body, "", styles.Faint("press any key to close"))
}
