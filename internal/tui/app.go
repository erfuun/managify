// Package tui is Managify's terminal UI. It is the only package that knows
// about Bubble Tea; all real work happens in the packages it calls.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/sshx"
)

// Deps are Managify's services. A nil Pool means no connections at all:
// no status probes, and the status column shows "? unknown".
type Deps struct {
	Inv     *inventory.Inventory
	Secrets secrets.Store
	Session *secrets.Session
	DataDir string
	Now     func() time.Time

	Pool          *sshx.Pool
	Creds         *sshx.Creds
	Prompts       *sshx.Queue
	ProbeInterval time.Duration // 0 → 30s
}

type screen int

const (
	screenList screen = iota
	screenForm
)

type Model struct {
	d             Deps
	screen        screen
	width, height int
	showHelp      bool
	footer        string // one-line message under the list, e.g. an error

	cursor        int
	selected      map[string]bool // server ID → selected
	confirmDelete string          // server ID awaiting y/n

	form formModel

	// ctx ends when the app quits, stopping probes and the prompt reader.
	ctx    context.Context
	cancel context.CancelFunc
	prompt *promptModel // open credential or host-key question, above any screen

	status      map[string]serverStatus
	probing     map[string]bool
	probeGen    map[string]int
	needsAction map[string]bool // last attempt needs the user; ticks skip it
}

func New(d Deps) Model {
	if d.Now == nil {
		d.Now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return Model{
		d: d, selected: map[string]bool{}, ctx: ctx, cancel: cancel,
		status: map[string]serverStatus{}, probing: map[string]bool{},
		probeGen: map[string]int{}, needsAction: map[string]bool{},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.waitPrompt(), m.probeAll(), m.probeTick())
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	m.cancel()
	return m, tea.Quit
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case promptMsg:
		return m.openPrompt(msg.p)
	case promptGoneMsg:
		if m.prompt != nil && m.prompt.p == msg.p {
			close(m.prompt.answered)
			m.prompt = nil
			return m, m.waitPrompt()
		}
		return m, nil
	case tea.KeyMsg:
		if m.prompt != nil {
			return m.updatePrompt(msg)
		}
	case statusMsg:
		return m.applyStatus(msg), nil
	case probeTickMsg:
		return m, tea.Batch(m.probeAll(), m.probeTick())
	case serverSavedMsg:
		m.forgetServer(msg.Server.ID)
		return m, m.retry(msg.Server)
	case serverDeletedMsg:
		m.forgetServer(msg.ID)
		return m, nil
	}
	switch m.screen {
	case screenList:
		return m.updateList(msg)
	case screenForm:
		return m.updateForm(msg)
	}
	return m, nil
}

func (m Model) View() string {
	var top string
	if m.prompt != nil {
		top = m.viewPrompt()
	}
	if m.showHelp {
		return top + m.viewHelp()
	}
	switch m.screen {
	case screenList:
		return top + m.viewList()
	case screenForm:
		return top + m.viewForm()
	}
	return top
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
