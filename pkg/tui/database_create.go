package tui

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/kontrolplane/pgui/pkg/pgui"
	"github.com/kontrolplane/pgui/pkg/tui/commands"
	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

// databaseCreateInput holds the form's values. The form writes to it through pointers, so it
// lives outside the model, which is copied on every update.
type databaseCreateInput struct {
	name     string
	owner    string
	template string
}

func (i *databaseCreateInput) dirty() bool {
	return i.name != "" || i.owner != "" || i.template != ""
}

type databaseCreateState struct {
	input *databaseCreateInput
	form  *huh.Form
}

// formWidth is the width of the form, centred in the content area.
func formWidth() int { return min(100, contentWidth-8) }

// formHeight is the height the form gets below its header.
func formHeight() int { return contentHeight - 4 }

func validateDatabaseName(s string) error {
	switch s = strings.TrimSpace(s); {
	case s == "":
		return errors.New("a database needs a name")
	case len(s) > 63:
		return errors.New("names are at most 63 bytes")
	}
	return nil
}

func newDatabaseCreateForm(input *databaseCreateInput) *huh.Form {
	return huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("name").
			Description("taken as it is typed, case included").
			Value(&input.name).
			Validate(validateDatabaseName),
		huh.NewInput().
			Title("owner").
			Description("the role that owns it, empty for the current user").
			Value(&input.owner),
		huh.NewInput().
			Title("template").
			Description("the database it is copied from, empty for template1").
			Placeholder("template1").
			Value(&input.template),
	)).
		WithTheme(styles.FormTheme()).
		WithShowHelp(false).
		WithWidth(formWidth()).
		WithHeight(formHeight()).
		WithShowErrors(false)
}

func (m model) DatabaseCreateSwitchPage() (model, tea.Cmd) {
	m.error = ""
	m.state.databaseCreate.input = &databaseCreateInput{}
	return m.openDatabaseCreateForm()
}

// openDatabaseCreateForm shows a new form on the current input, which keeps what was entered.
func (m model) openDatabaseCreateForm() (model, tea.Cmd) {
	c := &m.state.databaseCreate
	c.form = newDatabaseCreateForm(c.input)
	return m.SwitchPage(databaseCreate), c.form.Init()
}

// reopenDatabaseCreate returns to the form after the database could not be created, keeping the input.
func (m model) reopenDatabaseCreate(reason string) (model, tea.Cmd) {
	m, cmd := m.openDatabaseCreateForm()
	m.error = reason
	return m, cmd
}

func (m model) DatabaseCreateView() string {
	c := m.state.databaseCreate
	if c.form == nil {
		return "loading…"
	}
	var problem string
	if errs := c.form.Errors(); len(errs) > 0 {
		problem = styles.Render(styles.S("✗ ", styles.ToneDanger), styles.S(truncate(errs[0].Error(), formWidth()-2), styles.ToneDanger))
	}
	header := lipgloss.JoinVertical(lipgloss.Left,
		styles.Render(styles.B("● ", styles.ToneAccent), styles.B("new database", styles.ToneText)),
		lipgloss.NewStyle().Foreground(styles.P.Rule).Render(strings.Repeat("─", formWidth())),
		problem,
	)
	return lipgloss.Place(contentWidth, contentHeight, lipgloss.Center, lipgloss.Top, lipgloss.JoinVertical(lipgloss.Left, header, c.form.View()))
}

func (m model) DatabaseCreateUpdate(msg tea.Msg) (model, tea.Cmd) {
	c := &m.state.databaseCreate
	if c.form == nil || m.loading {
		return m, nil
	}
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && key.Matches(keyMsg, m.keys.Back) {
		if c.input.dirty() && !m.armed {
			m.armed = true
			return m.setStatus(discardPrompt, styles.ToneWarning)
		}
		return m.DatabaseOverviewGoBack()
	}

	form, cmd := c.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		c.form = f
	}
	switch c.form.State {
	case huh.StateCompleted:
		if m, refused := m.refuseOffline("created"); refused {
			return m.reopenDatabaseCreate(m.error)
		}
		cfg := pgui.DatabaseConfig{
			Name:     strings.TrimSpace(c.input.name),
			Owner:    strings.TrimSpace(c.input.owner),
			Template: strings.TrimSpace(c.input.template),
		}
		m.busy, m.loading, m.loadingMsg = true, true, "creating database "+cfg.Name+"…"
		return m, commands.CreateDatabase(m.context, m.pool, cfg)
	case huh.StateAborted:
		return m.DatabaseOverviewGoBack()
	}
	return m, cmd
}
