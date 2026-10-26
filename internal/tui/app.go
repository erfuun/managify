// Package tui is Managify's terminal UI. It is the only package that knows
// about Bubble Tea; all real work happens in the packages it calls.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"managify/internal/inventory"
	"managify/internal/secrets"
)

type Deps struct {
	Inv     *inventory.Inventory
	Secrets secrets.Store
	Session *secrets.Session
	DataDir string
	Now     func() time.Time
}

type screen int

const (
	screenList screen = iota
)

type Model struct {
	d             Deps
	screen        screen
	width, height int
	showHelp      bool
	footer        string // one-line message under the list, e.g. an error

	cursor   int
	selected map[string]bool // server ID → selected
}

func New(d Deps) Model {
	if d.Now == nil {
		d.Now = time.Now
	}
	return Model{d: d, selected: map[string]bool{}}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = ws.Width, ws.Height
		return m, nil
	}
	switch m.screen {
	case screenList:
		return m.updateList(msg)
	}
	return m, nil
}

func (m Model) View() string {
	if m.showHelp {
		return m.viewHelp()
	}
	switch m.screen {
	case screenList:
		return m.viewList()
	}
	return ""
}

// Targets is what an action applies to: the selected servers in list order,
// else the server under the cursor, else nothing.
func (m Model) Targets() []inventory.Server {
	var out []inventory.Server
	for _, s := range m.d.Inv.Servers {
		if m.selected[s.ID] {
			out = append(out, s)
		}
	}
	if len(out) > 0 {
		return out
	}
	if s, ok := m.cursorServer(); ok {
		return []inventory.Server{s}
	}
	return nil
}

func (m Model) cursorServer() (inventory.Server, bool) {
	servers := m.d.Inv.Servers
	if len(servers) == 0 {
		return inventory.Server{}, false
	}
	return servers[clamp(m.cursor, 0, len(servers)-1)], true
}

func (m Model) viewWidth() int {
	if m.width <= 0 {
		return 80
	}
	return m.width
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
