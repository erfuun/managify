package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/snippets"
)

// snippetList is the s screen: pick a snippet to run on the targets.
type snippetList struct {
	cursor        int
	confirmDelete string // snippet name awaiting y/n
	err           string
}

type snipField int

const (
	sfName snipField = iota
	sfCommand
	sfSudo
	sfLocal
	sfRemote
	nSnipFields
)

var snipLabels = [nSnipFields]string{"Name", "Command", "Sudo", "Upload file", "Upload to"}

type snippetForm struct {
	editName string                       // "" when adding
	inputs   [nSnipFields]textinput.Model // sfSudo is a toggle, not an input
	sudo     bool
	focus    snipField
	err      string
	back     screen // where esc and save return to
}

func newSnippetForm(sn *snippets.Snippet, back screen) snippetForm {
	f := snippetForm{back: back}
	for _, id := range []snipField{sfName, sfCommand, sfLocal, sfRemote} {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 4096
		ti.Width = 60
		f.inputs[id] = ti
	}
	f.inputs[sfLocal].Placeholder = "(optional) local file to upload first"
	f.inputs[sfRemote].Placeholder = "(optional) remote path; empty is your home"
	if sn != nil {
		f.inputs[sfName].SetValue(sn.Name)
		f.inputs[sfCommand].SetValue(sn.Command)
		f.sudo = sn.Sudo
		if sn.Upload != nil {
			f.inputs[sfLocal].SetValue(sn.Upload.Local)
			f.inputs[sfRemote].SetValue(sn.Upload.Remote)
		}
	}
	f.inputs[sfName].Focus()
	return f
}

func (f *snippetForm) moveFocus(delta int) tea.Cmd {
	f.focus = snipField(clamp(int(f.focus)+delta, 0, int(nSnipFields)-1))
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	if f.focus != sfSudo {
		return f.inputs[f.focus].Focus()
	}
	return nil
}

func (m Model) openSnippets() (tea.Model, tea.Cmd) {
	m.snipList = snippetList{cursor: clamp(m.snipList.cursor, 0, max(len(m.d.Snippets.Snippets)-1, 0))}
	m.screen = screenSnippets
	return m, nil
}

func (m Model) updateSnippets(msg tea.Msg) (tea.Model, tea.Cmd) {
	l := &m.snipList
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	all := m.d.Snippets.Snippets
	if l.confirmDelete != "" {
		name := l.confirmDelete
		l.confirmDelete = ""
		if km.String() == "y" {
			l.err = m.saveSnippets(func(s *snippets.Store) error { return s.Delete(name) })
			l.cursor = clamp(l.cursor, 0, max(len(m.d.Snippets.Snippets)-1, 0))
		}
		return m, nil
	}
	l.err = ""
	switch km.String() {
	case "esc":
		m.screen = screenList
	case "up", "k":
		l.cursor = max(l.cursor-1, 0)
	case "down", "j":
		l.cursor = min(l.cursor+1, max(len(all)-1, 0))
	case "n":
		return m.openSnippetForm(nil, screenSnippets)
	case "e":
		if len(all) > 0 {
			sn := all[l.cursor]
			return m.openSnippetForm(&sn, screenSnippets)
		}
	case "d":
		if len(all) > 0 {
			l.confirmDelete = all[l.cursor].Name
		}
	case "enter":
		if len(all) == 0 {
			return m, nil
		}
		t := m.Targets()
		if len(t) == 0 || m.d.Pool == nil {
			l.err = "Add a server first."
			return m, nil
		}
		return m.startRun(all[l.cursor].Spec(), t)
	}
	return m, nil
}

// saveSnippets applies change and writes snippets.yaml, returning the error
// to show. Like the server form, a failed write keeps the change in memory.
func (m Model) saveSnippets(change func(*snippets.Store) error) string {
	if err := change(m.d.Snippets); err != nil {
		return err.Error()
	}
	if err := m.d.Snippets.Save(); err != nil {
		return "could not save snippets.yaml: " + err.Error()
	}
	return ""
}

func (m Model) viewSnippets() string {
	l := m.snipList
	var b strings.Builder
	b.WriteString(" " + titleStyle.Render("Snippets") + "\n\n")
	all := m.d.Snippets.Snippets
	if len(all) == 0 {
		b.WriteString(" " + hintStyle.Render("No snippets yet. Press n to add one.") + "\n")
	}
	width := 0
	for _, sn := range all {
		width = max(width, len([]rune(sn.Name)))
	}
	for i, sn := range all {
		marker := "  "
		if i == l.cursor {
			marker = "› "
		}
		var tags []string
		if sn.Sudo {
			tags = append(tags, "[sudo]")
		}
		if sn.Upload != nil {
			tags = append(tags, "[upload]")
		}
		line := fmt.Sprintf(" %s%-*s  %s", marker, width, sn.Name, sn.Command)
		if len(tags) > 0 {
			line += "  " + hintStyle.Render(strings.Join(tags, " "))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	switch {
	case l.confirmDelete != "":
		b.WriteString(" " + errorStyle.Render("Delete "+l.confirmDelete+"? (y/n)") + "\n")
	case l.err != "":
		b.WriteString(" " + errorStyle.Render(l.err) + "\n")
	}
	if t := m.Targets(); len(t) > 0 {
		b.WriteString(" " + hintStyle.Render("enter runs on "+serverNames(t)) + "\n")
	}
	b.WriteString(" " + hintStyle.Render("↑/↓ move · enter run · n new · e edit · d delete · esc back") + "\n")
	return b.String()
}

func (m Model) openSnippetForm(sn *snippets.Snippet, back screen) (tea.Model, tea.Cmd) {
	m.snipForm = newSnippetForm(sn, back)
	if sn != nil && back == screenSnippets {
		m.snipForm.editName = sn.Name
	}
	m.screen = screenSnippetForm
	return m, textinput.Blink
}

// saveRunAsSnippet opens the snippet form with the finished run's spec.
func (m Model) saveRunAsSnippet() (tea.Model, tea.Cmd) {
	r := m.run
	switch {
	case m.d.Snippets == nil:
		return m, nil
	case r.running > 0:
		r.hint = "Wait for the run to finish, then ctrl+s."
		return m, nil
	case r.spec.Download != "":
		r.hint = "A download can't be saved as a snippet."
		return m, nil
	}
	sn := snippets.Snippet{Command: r.spec.Command, Sudo: r.spec.Sudo}
	if u := r.spec.Upload; u != nil {
		sn.Upload = &snippets.Upload{Local: u.Local, Remote: u.Remote}
	}
	return m.openSnippetForm(&sn, screenRun)
}

func (m Model) updateSnippetForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := &m.snipForm
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		if f.focus != sfSudo {
			f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		}
		return m, cmd
	}
	// Match key types, not strings, so a key event carrying text is never navigation.
	switch km.Type {
	case tea.KeyEsc:
		m.screen = f.back
		return m, nil
	case tea.KeyCtrlS:
		return m.submitSnippetForm()
	case tea.KeyTab, tea.KeyDown:
		return m, f.moveFocus(1)
	case tea.KeyShiftTab, tea.KeyUp:
		return m, f.moveFocus(-1)
	case tea.KeyEnter:
		if f.focus == nSnipFields-1 {
			return m.submitSnippetForm()
		}
		return m, f.moveFocus(1)
	}
	if f.focus == sfSudo {
		if km.Type == tea.KeySpace || km.Type == tea.KeyLeft || km.Type == tea.KeyRight || km.String() == " " {
			f.sudo = !f.sudo
		}
		return m, nil
	}
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
	return m, cmd
}

func (m Model) submitSnippetForm() (tea.Model, tea.Cmd) {
	f := &m.snipForm
	sn := snippets.Snippet{
		Name:    f.inputs[sfName].Value(),
		Command: f.inputs[sfCommand].Value(),
		Sudo:    f.sudo,
	}
	local, remote := strings.TrimSpace(f.inputs[sfLocal].Value()), strings.TrimSpace(f.inputs[sfRemote].Value())
	if local == "" && remote != "" {
		f.err = "an upload needs a local file"
		return m, nil
	}
	if local != "" {
		sn.Upload = &snippets.Upload{Local: local, Remote: remote}
	}
	f.err = m.saveSnippets(func(s *snippets.Store) error {
		if f.editName != "" {
			return s.Update(f.editName, sn)
		}
		return s.Add(sn)
	})
	if f.err != "" {
		return m, nil
	}
	name := strings.TrimSpace(sn.Name)
	if f.back == screenRun && m.run != nil {
		m.run.hint = "Saved snippet " + name
	}
	for i, s := range m.d.Snippets.Snippets {
		if s.Name == name {
			m.snipList.cursor = i
		}
	}
	m.screen = f.back
	return m, nil
}

func (m Model) viewSnippetForm() string {
	f := m.snipForm
	var b strings.Builder
	title := "New snippet"
	switch {
	case f.editName != "":
		title = "Edit " + f.editName
	case f.back == screenRun:
		title = "Save as snippet"
	}
	b.WriteString(" " + titleStyle.Render(title) + "\n\n")
	for id := sfName; id < nSnipFields; id++ {
		marker := "  "
		if id == f.focus {
			marker = "› "
		}
		val := ""
		if id == sfSudo {
			val = "[ ] run with sudo"
			if f.sudo {
				val = "[x] run with sudo"
			}
		} else {
			val = f.inputs[id].View()
		}
		fmt.Fprintf(&b, " %s%-12s %s\n", marker, snipLabels[id], val)
	}
	b.WriteString("\n")
	if f.err != "" {
		b.WriteString(" " + errorStyle.Render(f.err) + "\n")
	}
	b.WriteString(" " + hintStyle.Render("tab/↓ next · shift+tab/↑ back · space toggle · ctrl+s or enter on last field saves · esc cancel") + "\n")
	return b.String()
}
