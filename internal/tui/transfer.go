package tui

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/job"
)

type uploadStage int

const (
	stagePick uploadStage = iota
	stageRemote
	stageThen
)

// uploadModel walks u through: pick a file, a remote path, then an
// optional command to run after the upload.
type uploadModel struct {
	targets []inventory.Server
	stage   uploadStage
	picker  filepicker.Model
	local   string
	remote  textinput.Model
	then    textinput.Model
	sudo    bool
}

// downloadModel is g's remote path input.
type downloadModel struct {
	targets []inventory.Server
	remote  textinput.Model
}

func newLineInput(prompt string, width int) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	in.CharLimit = 4096
	in.Width = max(width-6, 20)
	return in
}

func (m Model) openUpload(targets []inventory.Server) (tea.Model, tea.Cmd) {
	dir := m.lastLocalDir
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
	}
	fp := filepicker.New()
	fp.CurrentDirectory = dir
	fp.AutoHeight = false
	fp.SetHeight(max(m.viewHeight()-6, 5))
	m.upload = uploadModel{targets: targets, picker: fp}
	m.screen = screenUpload
	return m, fp.Init()
}

func (m Model) updateUpload(msg tea.Msg) (tea.Model, tea.Cmd) {
	u := &m.upload
	km, isKey := msg.(tea.KeyMsg)
	if isKey && km.Type == tea.KeyEsc {
		// esc goes back a step everywhere, not up a directory in the picker.
		if u.stage == stagePick {
			m.screen = screenList
			return m, nil
		}
		u.stage--
		return m, u.focus()
	}
	switch u.stage {
	case stagePick:
		var cmd tea.Cmd
		u.picker, cmd = u.picker.Update(msg)
		if ok, p := u.picker.DidSelectFile(msg); ok {
			u.local = p
			m.lastLocalDir = u.picker.CurrentDirectory
			u.remote = newLineInput("Remote path: ", m.viewWidth())
			dir := u.targets[0].LastRemoteDir
			if dir == "" {
				dir = "/tmp/"
			}
			u.remote.SetValue(dir)
			u.then = newLineInput("$ ", m.viewWidth())
			u.stage = stageRemote
			return m, u.focus()
		}
		return m, cmd
	case stageRemote:
		if isKey && km.Type == tea.KeyEnter {
			u.stage = stageThen
			return m, u.focus()
		}
		var cmd tea.Cmd
		u.remote, cmd = u.remote.Update(msg)
		return m, cmd
	default:
		if isKey {
			switch km.Type {
			case tea.KeyCtrlO:
				u.sudo = !u.sudo
				return m, nil
			case tea.KeyEnter:
				spec := job.Spec{
					Upload:  &job.Upload{Local: u.local, Remote: strings.TrimSpace(u.remote.Value())},
					Command: strings.TrimSpace(u.then.Value()),
				}
				spec.Sudo = u.sudo && spec.Command != ""
				return m.startRun(spec, u.targets)
			}
		}
		var cmd tea.Cmd
		u.then, cmd = u.then.Update(msg)
		return m, cmd
	}
}

func (u *uploadModel) focus() tea.Cmd {
	u.remote.Blur()
	u.then.Blur()
	switch u.stage {
	case stageRemote:
		return u.remote.Focus()
	case stageThen:
		return u.then.Focus()
	}
	return nil
}

func (m Model) viewUpload() string {
	u := m.upload
	var b strings.Builder
	b.WriteString(" " + titleStyle.Render("Upload to "+serverNames(u.targets)) + "\n\n")
	switch u.stage {
	case stagePick:
		b.WriteString(" " + hintStyle.Render(u.picker.CurrentDirectory) + "\n")
		b.WriteString(u.picker.View() + "\n")
		b.WriteString(" " + hintStyle.Render("↑/↓ move · →/enter open · ←/backspace up · enter on a file picks it · esc back") + "\n")
		return b.String()
	}
	b.WriteString(" File: " + u.local + "\n\n")
	b.WriteString(" " + u.remote.View() + "\n")
	b.WriteString(" " + hintStyle.Render("a path ending in / means into that folder; ~ is your home") + "\n\n")
	if u.stage == stageThen {
		sudo := "[ ] sudo"
		if u.sudo {
			sudo = "[x] sudo"
		}
		b.WriteString(" Then run (optional):\n")
		b.WriteString(" " + u.then.View() + "\n")
		b.WriteString(" " + sudo + "\n\n")
		b.WriteString(" " + hintStyle.Render("enter start · ctrl+o toggle sudo · esc back") + "\n")
		return b.String()
	}
	b.WriteString(" " + hintStyle.Render("enter next · esc back") + "\n")
	return b.String()
}

func (m Model) openDownload(targets []inventory.Server) (tea.Model, tea.Cmd) {
	m.download = downloadModel{targets: targets, remote: newLineInput("Remote file: ", m.viewWidth())}
	m.screen = screenDownload
	return m, m.download.remote.Focus()
}

func (m Model) updateDownload(msg tea.Msg) (tea.Model, tea.Cmd) {
	dl := &m.download
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.Type {
		case tea.KeyEsc:
			m.screen = screenList
			return m, nil
		case tea.KeyEnter:
			p := strings.TrimSpace(dl.remote.Value())
			if p == "" {
				return m, nil
			}
			return m.startRun(job.Spec{Download: p}, dl.targets)
		}
	}
	var cmd tea.Cmd
	dl.remote, cmd = dl.remote.Update(msg)
	return m, cmd
}

func (m Model) viewDownload() string {
	dl := m.download
	var b strings.Builder
	b.WriteString(" " + titleStyle.Render("Download from "+serverNames(dl.targets)) + "\n\n")
	b.WriteString(" " + dl.remote.View() + "\n\n")
	b.WriteString(" " + hintStyle.Render("saved to "+downloadsHint(m.d.DataDir)) + "\n")
	b.WriteString(" " + hintStyle.Render("enter download · esc back") + "\n")
	return b.String()
}

// rememberRemoteDir records where s's last upload went, for the next prefill.
// It saves the inventory directly: a full serverSavedMsg would drop the
// pooled connection for a change that does not affect it.
func (m *Model) rememberRemoteDir(id, final string) {
	s, ok := m.d.Inv.Get(id)
	if !ok {
		return
	}
	dir := path.Dir(final)
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	if s.LastRemoteDir == dir {
		return
	}
	s.LastRemoteDir = dir
	if err := m.d.Inv.Update(s); err != nil {
		m.footer = err.Error()
		return
	}
	if err := m.d.Inv.Save(); err != nil {
		m.footer = "could not save servers.yaml: " + err.Error()
	}
}

func downloadsHint(dataDir string) string {
	return filepath.Join(dataDir, "downloads", "<server>") + string(filepath.Separator)
}

func serverNames(servers []inventory.Server) string {
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}
