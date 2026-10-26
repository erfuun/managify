package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/sshx"
)

// promptMsg carries the next credential or host-key question.
type promptMsg struct{ p *sshx.Pending }

// promptGoneMsg means the asker stopped waiting for p.
type promptGoneMsg struct{ p *sshx.Pending }

type promptModel struct {
	p        *sshx.Pending
	input    textinput.Model
	answered chan struct{} // closed on reply, ends watchPrompt
}

// waitPrompt reads one prompt from the queue. Exactly one is outstanding at
// a time: it is issued again only after the open prompt is answered or gone.
func (m Model) waitPrompt() tea.Cmd {
	q, ctx := m.d.Prompts, m.ctx
	if q == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case p := <-q.C:
			return promptMsg{p}
		case <-ctx.Done():
			return nil
		}
	}
}

func (m Model) watchPrompt(pm promptModel) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		select {
		case <-pm.p.Done():
			return promptGoneMsg{pm.p}
		case <-pm.answered:
		case <-ctx.Done():
		}
		return nil
	}
}

func (m Model) openPrompt(p *sshx.Pending) (Model, tea.Cmd) {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 1024
	in.Width = 40
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '*'
	in.Focus()
	pm := &promptModel{p: p, input: in, answered: make(chan struct{})}
	m.prompt = pm
	return m, m.watchPrompt(*pm)
}

// updatePrompt takes every key while a prompt is open, so nothing typed into
// a password reaches the screen underneath.
func (m Model) updatePrompt(km tea.KeyMsg) (Model, tea.Cmd) {
	pm := m.prompt
	decline := km.Type == tea.KeyEsc || km.Type == tea.KeyCtrlC
	if pm.p.Req.Kind == sshx.PromptHostKey {
		switch {
		case km.String() == "y":
			return m.answer(sshx.PromptReply{Accepted: true})
		case decline || km.String() == "n":
			return m.answer(sshx.PromptReply{})
		}
		return m, nil
	}
	switch {
	case km.Type == tea.KeyEnter:
		return m.answer(sshx.PromptReply{Text: pm.input.Value(), Accepted: true})
	case decline:
		return m.answer(sshx.PromptReply{})
	}
	var cmd tea.Cmd
	pm.input, cmd = pm.input.Update(km)
	return m, cmd
}

func (m Model) answer(r sshx.PromptReply) (Model, tea.Cmd) {
	m.prompt.p.Reply(r)
	close(m.prompt.answered)
	m.prompt = nil
	return m, m.waitPrompt()
}

func (m Model) viewPrompt() string {
	req := m.prompt.p.Req
	s := req.Server
	var lines []string
	switch req.Kind {
	case sshx.PromptHostKey:
		lines = []string{
			fmt.Sprintf("New server %s (%s)", s.Name, s.Addr()),
			"Key fingerprint: " + req.Detail,
			"Trust and remember this key? (y/n)",
		}
	case sshx.PromptPassphrase:
		lines = []string{fmt.Sprintf("Passphrase for key %s (%s): %s", req.Detail, s.Name, m.prompt.input.View())}
	case sshx.PromptSudo:
		lines = []string{fmt.Sprintf("sudo password for %s@%s (%s): %s", s.User, s.Host, s.Name, m.prompt.input.View())}
	default:
		lines = []string{fmt.Sprintf("Password for %s@%s (%s): %s", s.User, s.Host, s.Name, m.prompt.input.View())}
	}
	hint := "enter submit · esc cancel"
	if req.Kind == sshx.PromptHostKey {
		hint = "y trust · n or esc cancel"
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(" " + titleStyle.Render(l) + "\n")
	}
	b.WriteString(" " + hintStyle.Render(hint) + "\n\n")
	return b.String()
}
