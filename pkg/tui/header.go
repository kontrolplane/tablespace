package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/tablespace/pkg/tui/styles"
)

// spread places left and right on one line of the given width.
func spread(left, right string, width int) string {
	gap := width - styledWidth(left) - styledWidth(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

const (
	headerIndent = 1
	headerGap    = 2 // the least room between the fact columns
	usageBar     = 10
)

// headerFact is one label and value in the header grid.
type headerFact struct {
	label string
	value cell
}

// factsWidth is the width facts need to show in full.
func factsWidth(facts []headerFact) int {
	labelWidth, valueWidth := 0, 0
	for _, f := range facts {
		labelWidth = max(labelWidth, len(f.label))
		w := 0
		for _, s := range f.value {
			w += ansi.StringWidth(s.Text)
		}
		valueWidth = max(valueWidth, w)
	}
	return labelWidth + 2 + valueWidth
}

// factColumn renders facts as rows with their labels aligned, filling width.
func factColumn(width int, facts ...headerFact) string {
	labelWidth := 0
	for _, f := range facts {
		labelWidth = max(labelWidth, len(f.label))
	}
	lines := make([]string, len(facts))
	for i, f := range facts {
		label := styles.Faint(f.label + strings.Repeat(" ", labelWidth-len(f.label)+2))
		lines[i] = label + renderCell(f.value, column{width: max(1, width-labelWidth-2)}, nil)
	}
	return strings.Join(lines, "\n")
}

// renderHeader draws a grid of connection, server, database and connection count facts.
func (m model) renderHeader() string {
	width := frameWidth - headerIndent
	connection, server, database, connections := m.connectionFacts(), m.serverFacts(), m.databaseFacts(), m.connectionsFacts()

	// The facts change every few seconds at most, while the header is drawn on every frame.
	var key strings.Builder
	fmt.Fprint(&key, width)
	for _, facts := range [][]headerFact{connection, server, database, connections} {
		key.WriteString("\x01")
		for _, f := range facts {
			key.WriteString("\x00" + f.label)
			for _, s := range f.value {
				fmt.Fprintf(&key, "\x00%s\x00%d%v", s.Text, s.Tone, s.Bold)
			}
		}
	}
	return headerMemo.get(key.String(), func() string {
		facts := factsGrid(width, connection, server, database, connections)
		if facts == "" {
			facts = factsGrid(width, nil, server, database, connections)
		}
		if facts == "" {
			facts = factsGrid(-1, nil, server, database, connections)
		}
		return lipgloss.NewStyle().MarginLeft(headerIndent).Render(facts)
	})
}

var headerMemo memo

// factsGrid lays the fact columns out across width, or returns "" when they do not fit. Spare
// room goes between the columns so the grid spans the header like the line above it. Without
// any, the server and then the connection column give up width, since their names and urls read
// fine cut short while the numbers do not, but never below a floor that keeps them readable. A nil column
// is left out, and a negative width lays the columns out whether they fit or not.
func factsGrid(width int, columns ...[]headerFact) string {
	columns = slices.DeleteFunc(columns, func(f []headerFact) bool { return f == nil })
	natural := make([]int, len(columns))
	floor := make([]int, len(columns))
	total := 0
	for i, facts := range columns {
		natural[i] = factsWidth(facts)
		floor[i] = natural[i]
		total += natural[i]
	}
	// The last two columns hold numbers, the ones before them names that read fine cut short.
	flexible := len(columns) - 2
	for i := range flexible {
		floor[i] = min(natural[i], factsLabelWidth(columns[i])+2+12)
	}

	gaps := len(columns) - 1
	spare := width - total
	if short := gaps*headerGap - spare; short > 0 && width >= 0 {
		room := 0
		for i := range flexible {
			room += natural[i] - floor[i]
		}
		if short > room {
			return ""
		}
		// The server column gives up its width first, the url is what tells servers apart.
		left := short
		for i := flexible - 1; i >= 0 && left > 0; i-- {
			give := min(left, natural[i]-floor[i])
			natural[i] -= give
			left -= give
		}
		spare = gaps * headerGap
	}
	spare = max(spare, gaps)

	rendered := make([]string, 0, 2*len(columns)-1)
	for i, facts := range columns {
		if i > 0 {
			gap := spare / gaps
			if i <= spare%gaps {
				gap++
			}
			rendered = append(rendered, strings.Repeat(" ", gap))
		}
		rendered = append(rendered, factColumn(natural[i], facts...))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

func factsLabelWidth(facts []headerFact) int {
	w := 0
	for _, f := range facts {
		w = max(w, len(f.label))
	}
	return w
}

// pendingValue stands in for facts the server has not reported yet.
var pendingValue = text("…", styles.ToneFaint)

func (m model) connectionFacts() []headerFact {
	user := text(orDash(m.info.User), styles.ToneBody)
	if m.serverOK && m.server.SSL {
		user = append(user, styles.S("  ssl", styles.ToneFaint))
	}
	return []headerFact{
		{"host", text(orDash(m.info.Host), styles.ToneBody)},
		{"user", user},
	}
}

func (m model) serverFacts() []headerFact {
	if !m.serverOK {
		return []headerFact{{"server", pendingValue}, {"uptime", pendingValue}}
	}
	s := m.server
	role := styles.S("primary", styles.ToneBody)
	if s.Replica {
		role = styles.S("replica", styles.ToneInfo)
	}
	server := cell{styles.S("postgres "+s.Version, styles.ToneBody), sep(), role}
	if s.ReadOnly {
		server = append(server, sep(), styles.S("read only", styles.ToneWarning))
	}
	return []headerFact{
		{"server", server},
		{"uptime", text(formatUptime(time.Since(s.Started)), styles.ToneBody)},
	}
}

func (m model) databaseFacts() []headerFact {
	if !m.serverOK {
		return []headerFact{{"database", pendingValue}, {"size", pendingValue}}
	}
	return []headerFact{
		{"database", cell{styles.B(m.server.Database, styles.ToneText)}},
		{"size", text(formatBytes(m.server.DatabaseSize), styles.ToneBody)},
	}
}

func (m model) connectionsFacts() []headerFact {
	if !m.serverOK {
		return []headerFact{{"connections", pendingValue}}
	}
	return []headerFact{{"connections", usage(m.server.Connections, m.server.MaxConnections)}}
}

// usage renders a meter of used against limit, followed by the numbers.
func usage(used, limit int) cell {
	ratio := 0.0
	if limit > 0 {
		ratio = float64(used) / float64(limit)
	}
	tone := styles.ToneAccent
	switch {
	case ratio >= 0.95:
		tone = styles.ToneDanger
	case ratio >= 0.8:
		tone = styles.ToneWarning
	}
	return append(cell(styles.Bar(ratio, usageBar, tone)),
		styles.S(" "+formatCount(uint64(used)), styles.ToneBody),
		styles.S(" / "+formatCount(uint64(limit)), styles.ToneFaint),
	)
}

// formatUptime renders how long the server runs in its two largest units, e.g. 3d 4h.
func formatUptime(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", d/(24*time.Hour), d%(24*time.Hour)/time.Hour)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", d/time.Hour, d%time.Hour/time.Minute)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return fmt.Sprintf("%ds", max(0, d/time.Second))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%s %s", formatCount(uint64(n)), noun)
	}
	return fmt.Sprintf("%s %ss", formatCount(uint64(n)), noun)
}

// formatRTT renders a round trip time. Zero means the round trip was shorter than the clock
// resolution, which happens against a nearby server on platforms with a coarse clock (Windows).
func formatRTT(d time.Duration) string {
	if d <= 0 {
		return "<1ms"
	}
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(100 * time.Microsecond).String()
}

// connectionState renders the live state of the connection with its round trip time, and
// whether refreshing is paused.
func (m model) connectionState() string {
	status := m.connectionStatus()
	if m.serverOK && !m.disconnected() {
		status = append(status, styles.S(" · rtt ", styles.ToneFaint), styles.S(formatRTT(m.server.RTT), styles.ToneBody))
	}
	if m.paused {
		status = append([]styles.Span{styles.S("⏸ paused", styles.ToneWarning), styles.S(" · ", styles.ToneFaint)}, status...)
	}
	return styles.Render(status...)
}

// connectionStatus renders whether the server answered the last round trip.
func (m model) connectionStatus() []styles.Span {
	switch {
	case m.pool == nil:
		return []styles.Span{styles.S("○ offline", styles.ToneMuted)}
	case m.disconnected():
		return []styles.Span{styles.S("● ", styles.ToneDanger), styles.S("disconnected", styles.ToneDanger)}
	}
	return []styles.Span{styles.S("● ", styles.ToneSuccess), styles.S("connected", styles.ToneBody)}
}

// maxCrumbWidth bounds the crumbs between the first and the last, so a long table name leaves
// room for the page it leads to.
const maxCrumbWidth = 32

// breadcrumb names where the current page sits, e.g. databases › shop › public.orders › rows.
func (m model) breadcrumb() []styles.Span {
	db := m.state.databaseDetails.database
	tbl := m.state.tableDetails.table.DisplayName()
	var trail []string
	switch m.page {
	case databaseOverview:
		trail = []string{m.state.databaseOverview.tab.String()}
	case databaseCreate:
		trail = []string{"databases", "new database"}
	case databaseDrop:
		trail = []string{"databases", "drop"}
	case backendDetails:
		trail = []string{"activity", fmt.Sprintf("pid %d", m.state.backendDetails.backend.PID)}
	case backendSignal:
		trail = []string{"activity", m.state.backendSignal.verb()}
	case databaseDetails:
		trail = []string{"databases", db, m.state.databaseDetails.tab.String()}
	case tableDetails:
		trail = []string{"databases", db, tbl, m.state.tableDetails.tab.String()}
	case tableDrop:
		trail = []string{"databases", db, tbl, m.state.tableDrop.verb()}
	case rowDetails:
		trail = []string{"databases", db, tbl, "rows", m.state.rowDetails.title()}
		if m.state.rowDetails.fromQuery {
			trail = []string{"databases", m.info.Database, "sql", m.state.rowDetails.title()}
		}
	case rowDelete:
		trail = []string{"databases", db, tbl, "rows", "delete"}
	case sqlEditor:
		trail = []string{"databases", m.info.Database, "sql"}
	}

	var spans []styles.Span
	for i, t := range trail {
		if i > 0 {
			spans = append(spans, styles.S(" › ", styles.ToneFaint))
		}
		if i > 0 && i < len(trail)-1 {
			t = truncate(t, maxCrumbWidth)
		}
		if i == len(trail)-1 {
			spans = append(spans, styles.B(t, styles.ToneText))
		} else {
			spans = append(spans, styles.S(t, styles.ToneMuted))
		}
	}
	return spans
}

// clip cuts s to height lines, ending lines wider than width in an ellipsis.
func clip(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:max(height, 0)]
	}
	for i, line := range lines {
		if styledWidth(line) > width {
			lines[i] = ansi.Truncate(line, width, "…")
		}
	}
	return strings.Join(lines, "\n")
}

// frame draws the rounded border around the content area, with a title set into the top edge
// and meta text into the bottom one.
func frame(title, meta, foot, body string) string {
	border := lipgloss.NewStyle().Foreground(styles.P.Rule)
	edge := func(n int) string {
		if n < 1 {
			n = 1
		}
		return border.Render(strings.Repeat("─", n))
	}

	metaWidth := 0
	if meta != "" {
		meta = " " + ansi.Truncate(meta, frameWidth/3, "…") + " "
		metaWidth = lipgloss.Width(meta)
	}
	title = ansi.Truncate(title, frameWidth-7-metaWidth, "…")
	top := border.Render("╭─ ") + title + " " +
		edge(frameWidth-5-lipgloss.Width(title)-1-metaWidth) + meta + border.Render("─╮")

	footWidth := 0
	if foot != "" {
		foot = " " + ansi.Truncate(foot, frameWidth-6, "…") + " "
		footWidth = lipgloss.Width(foot)
	}
	bottom := border.Render("╰") + edge(frameWidth-3-footWidth) + foot + border.Render("─╯")

	// The body fills the content area: cut to it, with every line centred in it as lipgloss.Place
	// would, and a blank line above and below. Each line is measured once, this runs every frame.
	lines := strings.Split(body, "\n")
	lines = lines[:min(len(lines), contentHeight)]
	side := border.Render("│")
	blank := side + strings.Repeat(" ", contentWidth) + side

	var b strings.Builder
	b.Grow(len(body) + (contentHeight+4)*(contentWidth+40))
	b.WriteString(top)
	b.WriteString("\n")
	b.WriteString(blank)
	for i := range contentHeight {
		b.WriteString("\n")
		b.WriteString(side)
		if i >= len(lines) {
			b.WriteString(strings.Repeat(" ", contentWidth))
		} else {
			line := lines[i]
			w := styledWidth(line)
			if w > contentWidth {
				line = ansi.Truncate(line, contentWidth, "…")
				w = styledWidth(line)
			}
			gap := max(0, contentWidth-w)
			b.WriteString(strings.Repeat(" ", gap/2))
			b.WriteString(line)
			b.WriteString(strings.Repeat(" ", gap-gap/2))
		}
		b.WriteString(side)
	}
	b.WriteString("\n")
	b.WriteString(blank)
	b.WriteString("\n")
	b.WriteString(bottom)
	return b.String()
}
