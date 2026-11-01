package tui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/kontrolplane/pgui/pkg/tui/commands"
	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

type databaseDropState struct {
	names   []string
	hidden  int // selected databases the overview filter hides, which are not dropped
	confirm typedConfirm
}

// DatabaseDropSwitchPage asks to type the name of the database, or how many are dropped, since a
// dropped database cannot be brought back.
func (m model) DatabaseDropSwitchPage() (model, tea.Cmd) {
	m.error = ""
	d := &m.state.databaseDrop
	want := fmt.Sprintf("drop %d databases", len(d.names))
	if len(d.names) == 1 {
		want = d.names[0]
	}
	var cmd tea.Cmd
	d.confirm, cmd = newTypedConfirm(want)
	return m.SwitchPage(databaseDrop), cmd
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func (m model) DatabaseDropView() string {
	d := m.state.databaseDrop
	prompt := []styles.Span{styles.S("drop ", styles.ToneBody), styles.B(plural(len(d.names), "database"), styles.ToneText), styles.S(" with everything in them?", styles.ToneBody)}
	if len(d.names) == 1 {
		prompt = []styles.Span{styles.S("drop the database ", styles.ToneBody), styles.B(dialogName(d.names[0]), styles.ToneText), styles.S(" with everything in it?", styles.ToneBody)}
	}
	body := []string{styles.Render(prompt...)}
	body = append(body, listBody(d.names, false)...)
	body = append(body, "", styles.Faint("this cannot be undone. the server refuses a database someone is connected to."))
	if d.hidden > 0 {
		body = append(body, styles.Faint(fmt.Sprintf("%s hidden by the filter %s not included.", plural(d.hidden, "selected database"), isAre(d.hidden))))
	}
	body = append(body, "", d.confirm.view())
	return dialog("drop database", styles.ToneDanger, body...)
}

func (m model) DatabaseDropUpdate(msg tea.Msg) (model, tea.Cmd) {
	d := &m.state.databaseDrop
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(keyMsg, m.keys.Back):
			return m.DatabaseOverviewGoBack()
		case key.Matches(keyMsg, m.keys.View):
			if !d.confirm.ok() {
				return m, nil
			}
			if m, refused := m.refuseOffline("dropped"); refused {
				return m, nil
			}
			m.busy, m.loading, m.loadingMsg = true, true, fmt.Sprintf("dropping %s…", plural(len(d.names), "database"))
			if len(d.names) == 1 {
				m.loadingMsg = "dropping " + d.names[0] + "…"
			}
			return m, commands.DropDatabases(m.context, m.pool, d.names)
		}
	}
	var cmd tea.Cmd
	d.confirm, cmd = d.confirm.update(msg)
	return m, cmd
}
