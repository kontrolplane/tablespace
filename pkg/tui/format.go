package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/tablespace/pkg/tui/styles"
)

const (
	timeFormat = "2006-01-02 15:04:05"
)

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format(timeFormat)
}

// formatAgo renders a timestamp relative to now, e.g. "3m ago".
func formatAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatCount(n uint64) string {
	s := strconv.FormatUint(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// compactCount renders a count exactly up to 9,999,999 and shortened past it, e.g. 12.3M or 4.5B,
// so counts fit the columns of a table. It rounds down, so a count never reads as more than it is.
func compactCount(n uint64) string {
	if n <= 9_999_999 {
		return formatCount(n)
	}
	v, unit := float64(n)/1e6, "M"
	for _, u := range []string{"B", "T", "Q"} {
		if v < 1000 {
			break
		}
		v, unit = v/1000, u
	}
	if v < 100 {
		return strconv.FormatFloat(math.Floor(v*10)/10, 'f', 1, 64) + unit
	}
	return strconv.FormatFloat(math.Floor(v), 'f', 0, 64) + unit
}

// compactDuration renders a duration without its zero units, e.g. 1h rather than 1h0m0s, and
// whole days as days.
func compactDuration(d time.Duration) string {
	if d > 0 && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

func truncate(s string, width int) string {
	return ansi.Truncate(s, width, "…")
}

// formatPayload pretty prints a JSON value and reports whether it is one, anything else is
// returned as it is.
func formatPayload(data []byte) (string, bool) {
	if !utf8.Valid(data) {
		return fmt.Sprintf("<binary, %d bytes>", len(data)), false
	}
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return string(data), false
	}
	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return string(data), false
	}
	return string(pretty), true
}

// maxShownPayload bounds the formatted value the details view draws. Wrapping and colouring
// megabytes on every open would stall the interface for a view that shows a screen at a time.
const maxShownPayload = 256 << 10

// payloadText is a value as the details view shows it: pretty printed when it is JSON, cleaned,
// and cut at a line boundary once it passes maxShownPayload.
type payloadText struct {
	text   string
	json   bool
	empty  bool
	hidden int // bytes of the formatted value past the cut
}

// newPayloadText prepares a value, pretty printed when asJSON is set and it parses.
func newPayloadText(data []byte, asJSON bool) payloadText {
	if len(data) == 0 {
		return payloadText{empty: true}
	}
	body, js := string(data), false
	if asJSON {
		body, js = formatPayload(data)
	}
	p := payloadText{json: js}
	if len(body) > maxShownPayload {
		cut := strings.LastIndexByte(body[:maxShownPayload], '\n')
		if cut <= 0 {
			cut = maxShownPayload
			for cut > 0 && !utf8.RuneStart(body[cut]) {
				cut--
			}
		}
		p.hidden = len(body) - cut
		body = body[:cut]
	}
	p.text = styles.CleanBlock(body)
	return p
}

// render wraps the value at width, or not at all when width is 0, and colours it.
func (p payloadText) render(width int) string {
	if p.empty {
		return styles.Faint("empty string")
	}
	text := p.text
	if width > 0 {
		// Between words where it can, so values stay whole. Every byte stays, so the colouring of
		// JSON reads the same text.
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			lines[i] = strings.Join(wrapLine(line, width), "\n")
		}
		text = strings.Join(lines, "\n")
	}
	var out string
	if p.json {
		out = highlightJSON(text)
	} else {
		var b strings.Builder
		paintLines(&b, styles.ToneBody, text)
		out = b.String()
	}
	if p.hidden > 0 {
		more := fmt.Sprintf("… %s more not shown, c copies the whole value", formatBytes(uint64(p.hidden)))
		if width > 0 {
			more = truncate(more, width)
		}
		out += "\n\n" + styles.Faint(more)
	}
	return out
}

// paintLines writes s in tone, styling each line on its own so a wrapped token keeps its colour
// on every line without being padded into a block.
func paintLines(b *strings.Builder, tone styles.Tone, s string) {
	prefix := sgr(styles.P.Color(tone), false, nil)
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteString(ansi.ResetStyle)
	}
}

// highlightJSON colours pretty printed JSON: keys carry the text, strings the accent, numbers and
// literals the warm ink, and punctuation recedes.
func highlightJSON(s string) string {
	var b strings.Builder
	paint := func(tone styles.Tone, t string) { paintLines(&b, tone, t) }

	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(s))
			k := j
			for k < len(s) && s[k] == ' ' {
				k++
			}
			if k < len(s) && s[k] == ':' {
				paint(styles.ToneText, s[i:j])
			} else {
				paint(styles.ToneAccent, s[i:j])
			}
			i = j
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(s) && strings.IndexByte("0123456789.eE+-", s[j]) >= 0 {
				j++
			}
			paint(styles.ToneWarm, s[i:j])
			i = j
		case c == 't' || c == 'f' || c == 'n':
			j := i
			for j < len(s) && s[j] >= 'a' && s[j] <= 'z' {
				j++
			}
			paint(styles.ToneWarm, s[i:j])
			i = max(j, i+1)
		case c == ' ' || c == '\n':
			b.WriteByte(c)
			i++
		default:
			paint(styles.ToneFaint, string(c))
			i++
		}
	}
	return b.String()
}

func initFilterInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = ""
	ti.CharLimit = 100
	ti.SetWidth(50)
	ti.SetStyles(styles.TextInput())
	return ti
}

// setRows replaces the rows of a table and returns the cursor clamped to the new row count.
func setRows(t *dataTable, rows []tableRow, cursor int) int {
	t.SetRows(rows)
	t.SetCursor(cursor)
	return t.Cursor()
}

func verticalDivider(height int) string {
	lines := make([]string, height)
	for i := range lines {
		lines[i] = "│"
	}
	return lipgloss.NewStyle().Foreground(styles.P.Rule).Render(strings.Join(lines, "\n"))
}

// tableCount is count for a table cell, shortened past 9,999,999 so it fits its column.
func tableCount(n uint64, tone styles.Tone) cell {
	if n == 0 {
		return text("0", styles.ToneFaint)
	}
	return text(compactCount(n), tone)
}
