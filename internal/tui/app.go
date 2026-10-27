// Package tui is Managify's terminal UI. It is the only package that knows
// about Bubble Tea; all real work happens in the packages it calls.
package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/snippets"
	"github.com/erfuun/managify/internal/sshx"
)

// Deps are Managify's services. A nil Pool means no connections at all:
// no status probes, and the status column shows "? unknown".
type Deps struct {
	Inv      *inventory.Inventory
	Secrets  secrets.Store
	Session  *secrets.Session
	DataDir  string
	Now      func() time.Time
	Snippets *snippets.Store // nil disables the snippets screen

	Pool          *sshx.Pool
	Creds         *sshx.Creds
	Prompts       *sshx.Queue
	ProbeInterval time.Duration // 0 → 30s
}

type screen int

const (
	screenList screen = iota
	screenForm
	screenCmdInput
	screenRun
	screenUpload
	screenDownload
	screenSnippets
	screenSnippetForm
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

	form     formModel
	cmdInput cmdInputModel
	run      *runModel // the latest run; kept after leaving its screen
	upload   uploadModel
	download downloadModel
	snipList snippetList
	snipForm snippetForm

	lastLocalDir string // where the picker last picked a file, this session

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
		m.layoutRun()
		return m, nil
	case runBatchMsg:
		return m.applyRunBatch(msg)
	case shellReadyMsg:
		return m.shellReady(msg)
	case shellDoneMsg:
		return m.shellDone(msg)
	case promptMsg:
		return m.openPrompt(msg.p)
	case promptGoneMsg:
		if m.prompt != nil && m.prompt.p == msg.p {
			m.closePrompt()
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
	case screenCmdInput:
		return m.updateCmdInput(msg)
	case screenRun:
		return m.updateRun(msg)
	case screenUpload:
		return m.updateUpload(msg)
	case screenDownload:
		return m.updateDownload(msg)
	case screenSnippets:
		return m.updateSnippets(msg)
	case screenSnippetForm:
		return m.updateSnippetForm(msg)
	}
	return m, nil
}

// View keeps the frame within the terminal height. Bubble Tea drops lines
// from the top of a frame that is too tall, which would hide an open prompt;
// cutting from the bottom keeps it on screen.
func (m Model) View() string {
	v := strings.TrimSuffix(m.view(), "\n")
	if m.height <= 0 {
		return v
	}
	lines := strings.Split(v, "\n")
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

func (m Model) view() string {
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
	case screenCmdInput:
		return top + m.viewCmdInput()
	case screenRun:
		return top + m.viewRun()
	case screenUpload:
		return top + m.viewUpload()
	case screenDownload:
		return top + m.viewDownload()
	case screenSnippets:
		return top + m.viewSnippets()
	case screenSnippetForm:
		return top + m.viewSnippetForm()
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

func (m Model) viewHeight() int {
	if m.height <= 0 {
		return 24
	}
	return m.height
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
