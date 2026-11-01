package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/commands"
	"github.com/kontrolplane/pgui/pkg/tui/messages"
	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

type editorFocus int

const (
	editorFocusInput editorFocus = iota
	editorFocusResults
)

// maxHistory bounds the queries the editor remembers for the session.
const maxHistory = 100

type sqlEditorState struct {
	input textarea.Model
	focus editorFocus

	running bool
	cancel  context.CancelFunc
	started time.Time
	gen     uint64

	result    *pgui.QueryResult
	errText   string // why the last run failed
	ran       string // the query the result is of
	table     dataTable
	colOffset int

	history      []string // newest last
	historyOpen  bool
	historyTable dataTable
}

func newSQLEditorState() sqlEditorState {
	input := textarea.New()
	input.Placeholder = "select * from …"
	input.ShowLineNumbers = true
	input.CharLimit = 0
	input.MaxHeight = 0
	input.SetStyles(styles.TextArea())
	return sqlEditorState{
		input:        input,
		table:        newDataTable(nil, contentWidth-4, minTableHeight),
		historyTable: newDataTable(historyColumns, contentWidth-12, minTableHeight),
	}
}

var historyColumns = []column{
	{title: "#", width: 4, right: true},
	{title: "query", width: 60, grow: 1},
}

// editorInputHeight is the height of the text area, a third of the content area.
func editorInputHeight() int {
	return max(5, contentHeight/3)
}

// historyWidth is the width of the history card's table, as wide as a dialog.
func historyWidth() int { return min(contentWidth-12, 100) }

// historyHeight fits the history card's table to the queries it lists, within the content area.
func historyHeight(n int) int { return max(minTableHeight, min(n, contentHeight-8)) }

// resultsHeight is the body rows of the results table, below the input and the status line.
func resultsHeight() int {
	return max(minTableHeight, contentHeight-editorInputHeight()-3-tableHeaderRows)
}

func (m model) resizeEditor() model {
	e := &m.state.sqlEditor
	e.input.SetWidth(contentWidth - 4)
	e.input.SetHeight(editorInputHeight())
	e.historyTable.setSize(historyWidth(), historyHeight(len(e.history)))
	return m.updateResultsTable()
}

// SQLEditorSwitchPage opens the editor, with sql in it when it is not empty. What the editor
// held before is kept in the history.
func (m model) SQLEditorSwitchPage(sql string) (model, tea.Cmd) {
	m.error = ""
	e := &m.state.sqlEditor
	if sql != "" {
		m = m.remember(e.input.Value())
		e.input.SetValue(sql)
		e.input.MoveToEnd()
	}
	e.focus = editorFocusInput
	e.historyOpen = false
	m = m.SwitchPage(sqlEditor).resizeEditor()
	return m, e.input.Focus()
}

// remember adds sql to the history, moving it to the end when it is there already.
func (m model) remember(sql string) model {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return m
	}
	e := &m.state.sqlEditor
	e.history = slices.DeleteFunc(slices.Clone(e.history), func(h string) bool { return h == sql })
	e.history = append(e.history, sql)
	if len(e.history) > maxHistory {
		e.history = e.history[len(e.history)-maxHistory:]
	}
	return m
}

// sqlEditorGoBack leaves the editor for the page it is about: the database, or the overview.
func (m model) sqlEditorGoBack() (model, tea.Cmd) {
	e := &m.state.sqlEditor
	e.input.Blur()
	if m.state.databaseDetails.database == m.info.Database && m.state.databaseDetails.tablesLoaded {
		return m.DatabaseDetailsGoBack()
	}
	return m.DatabaseOverviewGoBack()
}

// run sends what the editor holds to its session.
func (m model) run() (model, tea.Cmd) {
	e := &m.state.sqlEditor
	sql := strings.TrimSpace(e.input.Value())
	if sql == "" || e.running {
		return m, nil
	}
	if m, refused := m.refuseOffline("run"); refused {
		return m, nil
	}
	m = m.remember(sql)
	ctx, cancel := context.WithCancel(m.context)
	e.running, e.cancel, e.started = true, cancel, time.Now()
	e.gen++
	e.ran = sql
	return m, commands.RunQuery(ctx, m.session, sql, e.gen)
}

func (m model) applyQuery(msg messages.QueryRanMsg) (model, tea.Cmd) {
	e := &m.state.sqlEditor
	if msg.Gen != e.gen {
		return m, nil
	}
	if e.cancel != nil {
		e.cancel()
	}
	e.running, e.cancel = false, nil
	res := msg.Result
	e.result, e.errText, e.colOffset = &res, "", 0
	e.table.SetCursor(0)
	if msg.Err != nil {
		e.errText = errorText(msg.Err)
		if pgui.IsCanceled(msg.Err) {
			e.errText = "cancelled"
		}
	}
	return m.updateResultsTable(), nil
}

func (m model) updateResultsTable() model {
	e := &m.state.sqlEditor
	if e.result == nil {
		e.table = newDataTable(nil, contentWidth-4, resultsHeight())
		return m
	}
	fields := e.result.Fields
	rows := make([]pgui.Row, len(e.result.Rows))
	for i, r := range e.result.Rows {
		rows[i] = pgui.Row{Values: r}
	}
	e.colOffset = max(0, min(e.colOffset, len(fields)-1))
	var cols []column
	for i, f := range fields[min(e.colOffset, len(fields)):] {
		c := column{title: f.Name, width: fieldWidth(e.colOffset+i, f, rows), right: numeric(f.Type)}
		if i > 0 {
			c.drop = i
		}
		cols = append(cols, c)
	}
	// A left aligned last column takes the width left over, a number would drift away from the rest.
	if n := len(cols); n > 0 && !cols[n-1].right {
		cols[n-1].grow, cols[n-1].min = 1, cols[n-1].width
	}
	cursor := e.table.Cursor()
	e.table = newDataTable(cols, contentWidth-4, resultsHeight())
	if e.focus != editorFocusResults {
		e.table.Blur()
	}
	trows := make([]tableRow, len(rows))
	for i, r := range rows {
		row := make(tableRow, 0, len(cols))
		for j, v := range r.Values[min(e.colOffset, len(r.Values)):] {
			row = append(row, valueCell(v, fields[e.colOffset+j].Type))
		}
		trows[i] = row
	}
	e.table.empty = "no rows"
	setRows(&e.table, trows, cursor)
	return m
}

func (m model) editorHelp() [][2]string {
	e := m.state.sqlEditor
	switch {
	case e.running:
		return [][2]string{{"esc", "cancel"}}
	case e.historyOpen:
		return [][2]string{{"enter", "use"}, {"↑/↓", "move"}, {"esc", "close"}}
	case e.focus == editorFocusResults:
		return [][2]string{{"enter", "open row"}, {"←/→", "columns"}, {"c", "copy row"}, {"ctrl+s", "run"}, {"tab", "query"}, {"?", "help"}, {"q", "back"}}
	}
	return [][2]string{{"ctrl+s", "run"}, {"ctrl+r", "history"}, {"tab", "results"}, {"esc", "back"}}
}

func (m model) SQLEditorUpdate(msg tea.Msg) (model, tea.Cmd) {
	e := &m.state.sqlEditor
	keyMsg, isKey := msg.(tea.KeyPressMsg)

	if e.running {
		if isKey && key.Matches(keyMsg, m.keys.Back) && e.cancel != nil {
			e.cancel()
			return m.setStatus("cancelling…", styles.ToneWarning)
		}
		if isKey {
			return m, nil
		}
	}

	if e.historyOpen {
		if !isKey {
			return m, nil
		}
		switch {
		case key.Matches(keyMsg, m.keys.Back), key.Matches(keyMsg, m.keys.History):
			e.historyOpen = false
			return m, e.input.Focus()
		case key.Matches(keyMsg, m.keys.View):
			if i := len(e.history) - 1 - e.historyTable.Cursor(); i >= 0 && i < len(e.history) {
				e.input.SetValue(e.history[i])
				e.input.MoveToEnd()
			}
			e.historyOpen = false
			e.focus = editorFocusInput
			return m, e.input.Focus()
		}
		e.historyTable = e.historyTable.Update(keyMsg)
		return m, nil
	}

	if isKey {
		switch {
		case key.Matches(keyMsg, m.keys.Submit):
			return m.run()
		case key.Matches(keyMsg, m.keys.History):
			if len(e.history) == 0 {
				return m.setStatus("no queries run yet", styles.ToneWarning)
			}
			e.historyOpen = true
			rows := make([]tableRow, len(e.history))
			for i := range e.history {
				h := e.history[len(e.history)-1-i]
				rows[i] = tableRow{text(fmt.Sprint(len(e.history)-i), styles.ToneFaint), text(oneLine(h), styles.ToneBody)}
			}
			e.historyTable.setSize(historyWidth(), historyHeight(len(rows)))
			e.historyTable.SetRows(rows)
			e.historyTable.SetCursor(0)
			e.input.Blur()
			return m, nil
		case key.Matches(keyMsg, m.keys.SwitchTab):
			if e.focus == editorFocusInput {
				e.focus = editorFocusResults
				e.input.Blur()
				e.table.Focus()
				return m, nil
			}
			e.focus = editorFocusInput
			e.table.Blur()
			return m, e.input.Focus()
		case key.Matches(keyMsg, m.keys.Back):
			return m.sqlEditorGoBack()
		}
	}

	if e.focus == editorFocusResults {
		if !isKey {
			return m, nil
		}
		switch {
		case key.Matches(keyMsg, m.keys.Quit):
			return m.sqlEditorGoBack()
		case key.Matches(keyMsg, m.keys.Left):
			if e.colOffset > 0 {
				e.colOffset--
				return m.updateResultsTable(), nil
			}
		case key.Matches(keyMsg, m.keys.Right):
			shown := 0
			for _, c := range e.table.columns {
				if !c.hide {
					shown++
				}
			}
			if e.result != nil && e.colOffset+shown < len(e.result.Fields) {
				e.colOffset++
				return m.updateResultsTable(), nil
			}
		case key.Matches(keyMsg, m.keys.View):
			if r, ok := m.currentResultRow(); ok {
				return m.openRow(rowDetailsState{fields: e.result.Fields, types: resultTypes(e.result.Fields), row: r, fromQuery: true, index: e.table.Cursor()}), nil
			}
		case key.Matches(keyMsg, m.keys.CopyToClipboard):
			if r, ok := m.currentResultRow(); ok {
				return m, commands.CopyToClipboard(rowJSON(e.result.Fields, r))
			}
		default:
			e.table = e.table.Update(keyMsg)
		}
		return m, nil
	}

	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	return m, cmd
}

// resultTypes names the types of a result's fields that are known by their oid.
func resultTypes(fields []pgui.Field) []string {
	names := map[uint32]string{16: "boolean", 17: "bytea", 20: "bigint", 21: "smallint", 23: "integer", 25: "text", 114: "json", 700: "real", 701: "double precision", 1043: "character varying", 1082: "date", 1114: "timestamp", 1184: "timestamp with time zone", 1700: "numeric", 2950: "uuid", 3802: "jsonb"}
	types := make([]string, len(fields))
	for i, f := range fields {
		types[i] = names[f.Type]
	}
	return types
}

func (m model) currentResultRow() (pgui.Row, bool) {
	e := m.state.sqlEditor
	if e.result == nil || len(e.result.Rows) == 0 {
		return pgui.Row{}, false
	}
	return pgui.Row{Values: e.result.Rows[e.table.Cursor()]}, true
}

// resultLine says how the last run went: the tags of its statements, how long it took, and
// whether a transaction is left open.
func (m model) resultLine() string {
	e := m.state.sqlEditor
	width := contentWidth - 4
	switch {
	case e.running:
		return styles.Render(styles.S(m.spinner.View()+" ", styles.ToneAccent), styles.S("running for "+formatFor(e.started), styles.ToneMuted), styles.S("  esc cancels", styles.ToneFaint))
	case e.result == nil:
		return styles.Faint("ctrl+s runs the query, the results show here")
	}
	r := e.result
	var spans []styles.Span
	if e.errText != "" {
		// Without a result to show, the error has the results area to itself.
		first, _, _ := strings.Cut(e.errText, "\n")
		if r.Fields == nil {
			first = "failed"
		}
		spans = append(spans, styles.S("✗ ", styles.ToneDanger), styles.S(first, styles.ToneDanger))
	}
	for i, st := range r.Statements {
		if i > 0 || len(spans) > 0 {
			spans = append(spans, sep())
		}
		spans = append(spans, styles.S(strings.ToLower(st.Tag), styles.ToneBody))
	}
	spans = append(spans, sep(), styles.S(compactDuration(r.Duration.Round(time.Millisecond)), styles.ToneMuted))
	if r.Truncated() {
		spans = append(spans, sep(), styles.S(fmt.Sprintf("first %s of %s rows", formatCount(uint64(len(r.Rows))), formatCount(uint64(r.Total))), styles.ToneWarning))
	}
	if r.InTx {
		spans = append(spans, sep(), styles.S("transaction open", styles.ToneWarning))
	}
	return truncate(styles.Render(spans...), width)
}

func (m model) SQLEditorView() string {
	e := m.state.sqlEditor
	width := contentWidth - 4
	name := truncate(m.info.Database, width/2)
	db := styles.Render(styles.S("on "+name, styles.ToneFaint))
	if m.info.ReadOnly {
		db = styles.Render(styles.S("on "+name+" ", styles.ToneFaint), styles.S("read only", styles.ToneWarning))
	}
	header := styles.SectionHeaderWith(styles.Fg(styles.ToneText).Bold(true).Render("query"), db, width)
	if e.focus == editorFocusInput {
		header = styles.SectionHeaderWith(styles.Fg(styles.ToneAccent).Bold(true).Render("query"), db, width)
	}
	results := e.table.View()
	switch {
	case e.result == nil:
		results = ""
	case e.result.Fields == nil && e.errText == "":
		results = styles.Faint("the last statement returned no rows")
	}
	if e.errText != "" && (e.result == nil || e.result.Fields == nil) {
		results = clip(styles.Fg(styles.ToneDanger).Width(width).Render(styles.CleanBlock(e.errText)), width, resultsHeight()+tableHeaderRows)
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		header,
		e.input.View(),
		"",
		m.resultLine(),
		results,
	)
	page := lipgloss.PlaceHorizontal(width, lipgloss.Left, body)
	if e.historyOpen {
		title := styles.Fg(styles.ToneAccent).Bold(true).MarginBottom(1).Render("history")
		card := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(styles.P.RuleBold).Padding(1, 2).
			Render(title + "\n" + e.historyTable.View())
		return overlay(page, card)
	}
	return page
}
