package keys

import "charm.land/bubbles/v2/key"

// KeyMap holds the keybindings shared by the views. Moving through a table (up/down, j/k,
// pgup/pgdown) is handled by the table itself.
type KeyMap struct {
	Left            key.Binding
	Right           key.Binding
	Top             key.Binding
	Bottom          key.Binding
	Help            key.Binding
	View            key.Binding
	Select          key.Binding
	Filter          key.Binding
	Create          key.Binding
	Delete          key.Binding
	CopyToClipboard key.Binding
	Truncate        key.Binding
	Refresh         key.Binding
	Pause           key.Binding
	Follow          key.Binding
	Editor          key.Binding
	Where           key.Binding
	CancelQuery     key.Binding
	History         key.Binding
	SwitchTab       key.Binding
	PrevTab         key.Binding
	NextField       key.Binding
	PrevField       key.Binding
	Submit          key.Binding
	Yes             key.Binding
	No              key.Binding
	Back            key.Binding
	Quit            key.Binding
	ForceQuit       key.Binding
}

var Keys = KeyMap{
	Left: key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("← | h", "left"),
	),
	Right: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("→ | l", "right"),
	),
	Top: key.NewBinding(
		key.WithKeys("g", "home"),
		key.WithHelp("g", "top"),
	),
	Bottom: key.NewBinding(
		key.WithKeys("G", "end"),
		key.WithHelp("G", "bottom"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	View: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "view"),
	),
	Select: key.NewBinding(
		key.WithKeys("space"),
		key.WithHelp("space", "select"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Create: key.NewBinding(
		key.WithKeys("ctrl+n"),
		key.WithHelp("ctrl+n", "create"),
	),
	Delete: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "delete"),
	),
	CopyToClipboard: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "copy"),
	),
	Truncate: key.NewBinding(
		key.WithKeys("ctrl+p"),
		key.WithHelp("ctrl+p", "truncate"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Pause: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "pause refresh"),
	),
	Follow: key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "follow newest"),
	),
	Editor: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "sql editor"),
	),
	Where: key.NewBinding(
		key.WithKeys("w"),
		key.WithHelp("w", "where"),
	),
	CancelQuery: key.NewBinding(
		key.WithKeys("ctrl+x"),
		key.WithHelp("ctrl+x", "cancel query"),
	),
	History: key.NewBinding(
		key.WithKeys("ctrl+r"),
		key.WithHelp("ctrl+r", "history"),
	),
	SwitchTab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next tab"),
	),
	PrevTab: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "previous tab"),
	),
	NextField: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next field"),
	),
	PrevField: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "previous field"),
	),
	Submit: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("ctrl+s", "submit"),
	),
	Yes: key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "yes"),
	),
	No: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "no"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit"),
	),
	ForceQuit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "quit"),
	),
}
