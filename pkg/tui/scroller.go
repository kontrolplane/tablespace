package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tablespace/pkg/tui/styles"
)

// scroller shows a window of rendered lines, moved with the arrow keys, page keys and g/G.
type scroller struct {
	lines  []string
	offset int
}

func newScroller() scroller { return scroller{} }

func (s *scroller) setContent(content string) {
	s.lines = strings.Split(content, "\n")
	s.clamp(0)
}

// clamp keeps the window inside the content for a view of height lines.
func (s *scroller) clamp(height int) {
	s.offset = max(0, min(s.offset, len(s.lines)-max(height, 1)))
}

// update scrolls a view of height lines, and reports whether the key was one it takes.
func (s *scroller) update(msg tea.KeyPressMsg, height int) bool {
	switch msg.String() {
	case "up", "k":
		s.offset--
	case "down", "j":
		s.offset++
	case "pgup":
		s.offset -= height
	case "pgdown", "space":
		s.offset += height
	case "home", "g":
		s.offset = 0
	case "end", "G":
		s.offset = len(s.lines)
	default:
		return false
	}
	s.clamp(height)
	return true
}

// view renders height lines from the offset, the last one saying how much is left below.
func (s scroller) view(height int) string {
	s.clamp(height)
	end := min(len(s.lines), s.offset+height)
	lines := s.lines[s.offset:end]
	if end < len(s.lines) && len(lines) > 0 {
		lines = append(lines[:len(lines)-1:len(lines)-1], styles.Faint(fmt.Sprintf("… %d more lines", len(s.lines)-end+1)))
	}
	return strings.Join(lines, "\n")
}
