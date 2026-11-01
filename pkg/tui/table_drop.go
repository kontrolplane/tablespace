package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/tablespace/pkg/tablespace"
	"github.com/kontrolplane/tablespace/pkg/tui/commands"
	"github.com/kontrolplane/tablespace/pkg/tui/styles"
)

// tableDropState asks to type the name of a table before it is dropped or emptied, neither of
// which can be undone.
type tableDropState struct {
	table    tablespace.Table
	truncate bool
	confirm  typedConfirm
}

func (s tableDropState) verb() string {
	if s.truncate {
		return "truncate"
	}
	return "drop"
}

// tableDropOpen asks to drop the open table, or with truncate to remove all of its rows.
func (m model) tableDropOpen(truncate bool) (model, tea.Cmd) {
	if m, cmd, refused := m.refuseReadOnly(); refused {
		return m, cmd
	}
	t := m.state.tableDetails.table
	if truncate && !t.HoldsRows() {
		return m.setStatus("a "+t.Kind+" holds no rows of its own to truncate", styles.ToneWarning)
	}
	m.error = ""
	confirm, cmd := newTypedConfirm(t.DisplayName())
	m.state.tableDrop = tableDropState{table: t, truncate: truncate, confirm: confirm}
	return m.SwitchPage(tableDrop), cmd
}

func (m model) TableDropView() string {
	s := m.state.tableDrop
	name := styles.B(dialogName(s.table.DisplayName()), styles.ToneText)
	body := []string{styles.Render(styles.S("drop the "+s.table.Kind+" ", styles.ToneBody), name, styles.S("?", styles.ToneBody)),
		"", styles.Faint("this cannot be undone. the server refuses while anything depends on it, nothing else is dropped.")}
	if s.truncate {
		body = []string{styles.Render(styles.S("remove every row of the "+s.table.Kind+" ", styles.ToneBody), name, styles.S("?", styles.ToneBody)),
			"", styles.Faint("this cannot be undone. the server refuses while other tables reference it.")}
	}
	body = append(body, "", s.confirm.view())
	return dialog(s.verb()+" "+s.table.Kind, styles.ToneDanger, body...)
}

func (m model) TableDropUpdate(msg tea.Msg) (model, tea.Cmd) {
	s := &m.state.tableDrop
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(keyMsg, m.keys.Back):
			return m.TableDetailsGoBack()
		case key.Matches(keyMsg, m.keys.View):
			if !s.confirm.ok() {
				return m, nil
			}
			nothing := "dropped"
			if s.truncate {
				nothing = "truncated"
			}
			if m, refused := m.refuseOffline(nothing); refused {
				return m, nil
			}
			m.busy, m.loading = true, true
			if s.truncate {
				m.loadingMsg = "truncating " + s.table.DisplayName() + "…"
				return m, commands.TruncateTable(m.context, m.pool, s.table)
			}
			m.loadingMsg = "dropping " + s.table.DisplayName() + "…"
			return m, commands.DropTable(m.context, m.pool, s.table)
		}
	}
	var cmd tea.Cmd
	s.confirm, cmd = s.confirm.update(msg)
	return m, cmd
}
