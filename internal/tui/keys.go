package tui

import "github.com/charmbracelet/bubbles/key"

type listKeys struct {
	Up, Down, Select, Shell, Run, Upload, Download, Snippets key.Binding
	Add, Edit, Delete, Refresh, Help, Quit                   key.Binding
}

var keys = listKeys{
	Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "move up")),
	Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "move down")),
	Select:   key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select / unselect")),
	Shell:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open a shell")),
	Run:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "run a command")),
	Upload:   key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "upload a file")),
	Download: key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "download a file")),
	Snippets: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "snippets")),
	Add:      key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add a server")),
	Edit:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit server")),
	Delete:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete server")),
	Refresh:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh status")),
	Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "toggle help")),
	Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

func (k listKeys) all() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Select, k.Shell, k.Run, k.Upload, k.Download,
		k.Snippets, k.Add, k.Edit, k.Delete, k.Refresh, k.Help, k.Quit}
}
