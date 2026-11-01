package tui

import (
	"strings"

	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

// messageKeys are the fields a message is usually written in, which carry the text ink.
var messageKeys = []string{"msg", "message", "error", "err"}

func isMessageKey(key string) bool {
	for _, k := range messageKeys {
		if strings.EqualFold(key, k) {
			return true
		}
	}
	return false
}

// tintJSON tints a JSON value on one line, so its structure recedes behind the values: keys and
// punctuation are faint, values keep the body ink. It does not check the line is valid, a broken
// one is tinted as far as it reads.
func tintJSON(line string) cell {
	var c cell
	add := func(text string, tone styles.Tone) {
		if text == "" {
			return
		}
		if n := len(c); n > 0 && c[n-1].Tone == tone {
			c[n-1].Text += text
			return
		}
		c = append(c, styles.Span{Text: text, Tone: tone})
	}
	depth := 0
	key := ""
	for i := 0; i < len(line); {
		switch ch := line[i]; {
		case ch == '"':
			j := i + 1
			for j < len(line) && line[j] != '"' {
				if line[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(line))
			k := j
			for k < len(line) && line[k] == ' ' {
				k++
			}
			switch {
			case k < len(line) && line[k] == ':':
				key = line[i+1 : max(i+1, j-1)]
				add(line[i:j], styles.ToneFaint)
			case depth == 1 && isMessageKey(key):
				add(line[i:j], styles.ToneText)
			default:
				add(line[i:j], styles.ToneBody)
			}
			i = j
		case strings.IndexByte("{}[],:", ch) >= 0:
			switch ch {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
			add(string(ch), styles.ToneFaint)
			i++
		default:
			j := i + 1
			for j < len(line) && strings.IndexByte(`"{}[],:`, line[j]) < 0 {
				j++
			}
			add(line[i:j], styles.ToneBody)
			i = j
		}
	}
	return c
}
