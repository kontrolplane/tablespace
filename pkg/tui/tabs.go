package tui

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

// tabItem is a tab of a tab bar. A tab whose items are not loaded yet goes without a count.
type tabItem struct {
	name   string
	count  int
	loaded bool
	more   bool // there are more than the count, which only says how many are loaded
}

// renderTabs draws a tab bar with active highlighted, and the longest of the hints that fits the
// room the tabs leave.
func renderTabs(tabs []tabItem, active int, hints ...string) string {
	var bar string
	for i, t := range tabs {
		if i > 0 {
			bar += " "
		}
		num := ""
		if t.loaded {
			num = formatCount(uint64(t.count))
			if t.more {
				num += "+"
			}
			num += " "
		}
		if i == active {
			bg := styles.P.Selection
			bar += styles.S(" "+t.name+" ", styles.ToneText).On(bg) + styles.B(num, styles.ToneAccent).On(bg)
			continue
		}
		bar += styles.Render(styles.S(" "+t.name+" ", styles.ToneMuted), styles.S(num, styles.ToneFaint))
	}
	width := contentWidth - 4
	room := width - styledWidth(bar) - 4
	for _, hint := range hints {
		if ansi.StringWidth(hint) <= room {
			return spread(bar, styles.Faint(styles.Clean(hint)), width)
		}
	}
	return spread(bar, "", width)
}

// nextTab steps from tab by delta through n tabs, wrapping around.
func nextTab(tab, delta, n int) int {
	return ((tab+delta)%n + n) % n
}
