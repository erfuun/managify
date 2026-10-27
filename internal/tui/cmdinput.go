package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/job"
)

// cmdInputModel is the "run a command" prompt opened by r.
type cmdInputModel struct {
	input   textinput.Model
	sudo    bool
	targets []inventory.Server
}

func (m Model) openCmdInput(targets []inventory.Server) (tea.Model, tea.Cmd) {
	in := textinput.New()
	in.Prompt = "$ "
	in.CharLimit = 4096
	in.Width = max(m.viewWidth()-6, 20)
	m.cmdInput = cmdInputModel{input: in, targets: targets}
	m.screen = screenCmdInput
	return m, m.cmdInput.input.Focus()
}

func (m Model) updateCmdInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	ci := &m.cmdInput
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.Type {
		case tea.KeyEsc:
			m.screen = screenList
			return m, nil
		case tea.KeyCtrlO:
			ci.sudo = !ci.sudo
			return m, nil
		case tea.KeyEnter:
			cmd := strings.TrimSpace(ci.input.Value())
			if cmd == "" {
				return m, nil
			}
			return m.startRun(job.Spec{Command: cmd, Sudo: ci.sudo}, ci.targets)
		}
	}
	var cmd tea.Cmd
	ci.input, cmd = ci.input.Update(msg)
	return m, cmd
}

func (m Model) viewCmdInput() string {
	ci := m.cmdInput
	names := make([]string, len(ci.targets))
	for i, s := range ci.targets {
		names[i] = s.Name
	}
	sudo := "[ ] sudo"
	if ci.sudo {
		sudo = "[x] sudo"
	}
	var b strings.Builder
	b.WriteString(" " + titleStyle.Render("Run on "+strings.Join(names, ", ")) + "\n\n")
	b.WriteString(" " + ci.input.View() + "\n\n")
	b.WriteString(" " + sudo + "\n\n")
	b.WriteString(" " + hintStyle.Render("enter run · ctrl+o toggle sudo · esc back") + "\n")
	return b.String()
}
