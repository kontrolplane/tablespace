package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/commands"
	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

type backendDetailsState struct {
	backend pgui.Backend
	blocks  []int // the backends waiting on this one
	gone    bool  // it is no longer listed
	view    scroller
}

func (m model) BackendDetailsSwitchPage(b pgui.Backend) (model, tea.Cmd) {
	m.error = ""
	d := &m.state.backendDetails
	d.backend, d.gone = b, false
	d.blocks = pgui.Blocks(b.PID, m.state.databaseOverview.backends)
	d.view = newScroller()
	m = m.SwitchPage(backendDetails)
	m.refreshedAt = time.Now()
	m, cmd := m.scheduleRefresh()
	return m, cmd
}

// backendDetailsGoBack returns to the activity tab.
func (m model) backendDetailsGoBack() (model, tea.Cmd) {
	m.state.databaseOverview.tab = tabActivity
	m = m.focusOverviewTab()
	return m.DatabaseOverviewGoBack()
}

// syncBackendDetails takes the open backend from a new listing. A backend that ended stays on
// screen as it was last seen.
func (m model) syncBackendDetails() model {
	d := &m.state.backendDetails
	i := slices.IndexFunc(m.state.databaseOverview.backends, func(b pgui.Backend) bool { return b.PID == d.backend.PID })
	if i < 0 {
		d.gone = true
		return m
	}
	d.backend, d.gone = m.state.databaseOverview.backends[i], false
	d.blocks = pgui.Blocks(d.backend.PID, m.state.databaseOverview.backends)
	return m
}

func timeAndAgo(t time.Time) cell {
	if t.IsZero() {
		return text("-", styles.ToneFaint)
	}
	return cell{styles.S(formatTime(t), styles.ToneBody), styles.S("  "+formatAgo(t), styles.ToneFaint)}
}

func (m model) BackendDetailsView() string {
	d := m.state.backendDetails
	b := d.backend

	state := backendState(b)
	if d.gone {
		state = text("ended", styles.ToneMuted)
	}
	wait := text("-", styles.ToneFaint)
	if b.WaitEvent != "" {
		wait = cell{styles.S(b.WaitEventType+" ", styles.ToneFaint), styles.S(b.WaitEvent, styles.ToneBody)}
	}
	pids := func(p []int, tone styles.Tone) cell {
		if len(p) == 0 {
			return text("-", styles.ToneFaint)
		}
		return text(pidJoin(p), tone)
	}
	section := func(title string, first, compact bool) string {
		if compact {
			first = true
		}
		return panelSection(title, first, leftContentWidth)
	}
	render := func(compact bool) []string {
		return []string{
			section("backend", true, compact),
			panelRowSpans("pid", styles.B(fmt.Sprint(b.PID), styles.ToneText), sep(), styles.S(b.Type, styles.ToneMuted)),
			panelRowSpans("state", state...),
			panelRow("user", orDash(b.User)),
			panelRow("database", orDash(b.Database)),
			panelRow("application", orDash(b.Application)),
			panelRow("client", orDash(b.Client)),
			section("locks", false, compact),
			panelRowSpans("waiting on", wait...),
			panelRowSpans("blocked by", pids(b.BlockedBy, styles.ToneDanger)...),
			panelRowSpans("blocking", pids(d.blocks, styles.ToneWarning)...),
			section("timings", false, compact),
			panelRowSpans("backend start", timeAndAgo(b.BackendStart)...),
			panelRowSpans("transaction", timeAndAgo(b.XactStart)...),
			panelRowSpans("query start", timeAndAgo(b.QueryStart)...),
			panelRowSpans("state change", timeAndAgo(b.StateChange)...),
		}
	}
	left := strings.Join(render(false), "\n")
	if lipgloss.Height(left) > contentHeight {
		left = strings.Join(render(true), "\n")
	}

	query := styles.Faint("no query")
	if b.Query != "" {
		query = newPayloadText([]byte(b.Query), false).render(rightContentWidth)
	}
	view := d.view
	view.setContent(query)
	right := styles.SectionHeader("query", rightContentWidth, true) + "\n" + view.view(contentHeight-1)

	return splitPanels(clip(left, leftPanelWidth, contentHeight), right)
}

func (m model) BackendDetailsUpdate(msg tea.Msg) (model, tea.Cmd) {
	d := &m.state.backendDetails
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(keyMsg, m.keys.Back), key.Matches(keyMsg, m.keys.Quit):
		return m.backendDetailsGoBack()
	case key.Matches(keyMsg, m.keys.Refresh):
		if m.disconnected() {
			return m.setStatus(offlineStatus, styles.ToneWarning)
		}
		return m, commands.LoadBackends(m.context, m.pool)
	case key.Matches(keyMsg, m.keys.CopyToClipboard):
		if d.backend.Query != "" {
			return m, commands.CopyToClipboard(d.backend.Query)
		}
	case key.Matches(keyMsg, m.keys.CancelQuery), key.Matches(keyMsg, m.keys.Delete):
		if d.gone {
			return m.setStatus(fmt.Sprintf("pid %d already ended", d.backend.PID), styles.ToneWarning)
		}
		return m.BackendSignalSwitchPage([]int{d.backend.PID}, 0, key.Matches(keyMsg, m.keys.Delete), true)
	default:
		query := newPayloadText([]byte(d.backend.Query), false).render(rightContentWidth)
		d.view.setContent(query)
		d.view.update(keyMsg, contentHeight-1)
	}
	return m, nil
}

// backendSignalState asks to cancel the queries of backends, or terminate them.
type backendSignalState struct {
	pids        []int
	hidden      int
	terminate   bool
	fromDetails bool
	selected    int
}

func (s backendSignalState) verb() string {
	if s.terminate {
		return "terminate"
	}
	return "cancel query"
}

// title names the dialog: what it does, to what.
func (s backendSignalState) title() string {
	if s.terminate {
		return "terminate backend"
	}
	return "cancel query"
}

func (m model) BackendSignalSwitchPage(pids []int, hidden int, terminate, fromDetails bool) (model, tea.Cmd) {
	if m, cmd, refused := m.refuseReadOnly(); refused {
		return m, cmd
	}
	m.error = ""
	m.state.backendSignal = backendSignalState{pids: pids, hidden: hidden, terminate: terminate, fromDetails: fromDetails}
	return m.SwitchPage(backendSignal), nil
}

// backendSignalGoBack returns to where the dialog was asked from.
func (m model) backendSignalGoBack() (model, tea.Cmd) {
	if m.state.backendSignal.fromDetails {
		m.error = ""
		m = m.SwitchPage(backendDetails)
		return m, commands.LoadBackends(m.context, m.pool)
	}
	return m.backendDetailsGoBack()
}

func (m model) BackendSignalView() string {
	s := m.state.backendSignal
	target := styles.B(pidList(s.pids), styles.ToneText)
	prompt := []styles.Span{styles.S("cancel the running query of ", styles.ToneBody), target, styles.S("?", styles.ToneBody)}
	note := "the session stays open, a transaction it is in is rolled back."
	if s.terminate {
		prompt = []styles.Span{styles.S("terminate ", styles.ToneBody), target, styles.S("?", styles.ToneBody)}
		note = "the session is closed and its transaction rolled back, the client sees its connection drop."
	}
	var items []string
	for _, pid := range s.pids {
		items = append(items, m.backendLabel(pid))
	}
	body := listBody(items, false)
	if len(s.pids) == 1 {
		body = []string{"", styles.Faint(truncate(m.backendLabel(s.pids[0]), dialogTextWidth()))}
	}
	body = append(body, "", styles.Faint(note))
	if s.hidden > 0 {
		body = append(body, styles.Faint(fmt.Sprintf("%s hidden by the filter %s not included.", plural(s.hidden, "selected backend"), isAre(s.hidden))))
	}
	if slices.ContainsFunc(m.state.databaseOverview.backends, func(b pgui.Backend) bool { return b.Self && slices.Contains(s.pids, b.PID) }) {
		body = append(body, styles.Render(styles.S("one of them is a session of pgui itself.", styles.ToneWarning)))
	}
	return confirmDialog(s.title(), prompt, body, s.selected)
}

// backendLabel names a backend in a dialog by its pid, user and query.
func (m model) backendLabel(pid int) string {
	i := slices.IndexFunc(m.state.databaseOverview.backends, func(b pgui.Backend) bool { return b.PID == pid })
	if i < 0 {
		return fmt.Sprintf("pid %d", pid)
	}
	b := m.state.databaseOverview.backends[i]
	parts := []string{fmt.Sprintf("pid %d", pid)}
	if b.User != "" {
		parts = append(parts, b.User)
	}
	if q := oneLine(b.Query); q != "" {
		parts = append(parts, q)
	}
	return strings.Join(parts, " · ")
}

func (m model) BackendSignalUpdate(msg tea.Msg) (model, tea.Cmd) {
	s := &m.state.backendSignal
	switch m.confirmKey(msg, &s.selected) {
	case confirmYes:
		if m, refused := m.refuseOffline("signalled"); refused {
			return m, nil
		}
		m.busy, m.loading, m.loadingMsg = true, true, "terminating "+pidList(s.pids)+"…"
		if !s.terminate {
			m.loadingMsg = "cancelling the query of " + pidList(s.pids) + "…"
		}
		return m, commands.SignalBackends(m.context, m.pool, s.pids, s.terminate)
	case confirmNo:
		return m.backendSignalGoBack()
	}
	return m, nil
}
