package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
)

// serverSavedMsg follows a successful add or edit.
type serverSavedMsg struct{ Server inventory.Server }

// serverDeletedMsg follows a confirmed delete.
type serverDeletedMsg struct{ ID string }

type field int

const (
	fName field = iota
	fHost
	fPort
	fUser
	fAuth
	fKeyPath
	fSudoMode
	fSudoPw
	fPassword
	fSavePw
	fJump
	nFields
)

var fieldLabels = [nFields]string{"Name", "Host", "Port", "User", "Auth", "Key path", "Sudo password", "Sudo pw value", "Password", "Keychain", "Jump via"}

var sudoModes = []inventory.SudoMode{inventory.SudoNone, inventory.SudoSaved, inventory.SudoAsk}

type formModel struct {
	editID   string // "" when adding
	orig     inventory.Server
	inputs   [nFields]textinput.Model // only text fields are used
	auth     inventory.Auth
	sudoMode inventory.SudoMode
	savePw   bool
	// jumpIDs and jumpNames list the jump choices, "" and "none" first;
	// jump indexes them.
	jumpIDs   []string
	jumpNames []string
	jump      int
	focus     int // index into visible()
	err       string
}

func newForm(s *inventory.Server, jumps []inventory.Server) formModel {
	f := formModel{auth: inventory.AuthKey, sudoMode: inventory.SudoNone, savePw: true,
		jumpIDs: []string{""}, jumpNames: []string{"none"}}
	for _, j := range jumps {
		f.jumpIDs = append(f.jumpIDs, j.ID)
		f.jumpNames = append(f.jumpNames, j.Name)
		if s != nil && s.Jump == j.ID {
			f.jump = len(f.jumpIDs) - 1
		}
	}
	for _, id := range []field{fName, fHost, fPort, fUser, fKeyPath, fSudoPw, fPassword} {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 512
		ti.Width = 48
		f.inputs[id] = ti
	}
	for _, id := range []field{fSudoPw, fPassword} {
		f.inputs[id].EchoMode = textinput.EchoPassword
		f.inputs[id].EchoCharacter = '*'
	}
	f.inputs[fPort].SetValue("22")
	if s != nil {
		f.editID, f.orig = s.ID, *s
		f.inputs[fName].SetValue(s.Name)
		f.inputs[fHost].SetValue(s.Host)
		f.inputs[fPort].SetValue(strconv.Itoa(s.Port))
		f.inputs[fUser].SetValue(s.User)
		f.inputs[fKeyPath].SetValue(s.KeyPath)
		f.auth = s.Auth
		// A key server switched to password starts with saving on, like a new one.
		f.savePw = s.SavePassword || s.Auth == inventory.AuthKey
		if s.SudoPassword != "" {
			f.sudoMode = s.SudoPassword
		}
		f.inputs[fPassword].Placeholder = "(unchanged)"
		f.inputs[fSudoPw].Placeholder = "(unchanged)"
	}
	f.inputs[fName].Focus()
	return f
}

func (f formModel) visible() []field {
	v := []field{fName, fHost, fPort, fUser}
	// Shown only when there is a server to jump through.
	if len(f.jumpIDs) > 1 {
		v = append(v, fJump)
	}
	v = append(v, fAuth)
	if f.auth == inventory.AuthKey {
		v = append(v, fKeyPath, fSudoMode)
		if f.sudoMode == inventory.SudoSaved {
			v = append(v, fSudoPw)
		}
		return v
	}
	return append(v, fPassword, fSavePw)
}

func (f formModel) current() field { return f.visible()[f.focus] }

func (f *formModel) moveFocus(delta int) tea.Cmd {
	f.focus = clamp(f.focus+delta, 0, len(f.visible())-1)
	return f.refocus()
}

func (f *formModel) refocus() tea.Cmd {
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	if cur := f.current(); isText(cur) {
		return f.inputs[cur].Focus()
	}
	return nil
}

func isText(id field) bool {
	return id != fAuth && id != fSudoMode && id != fSavePw && id != fJump
}

func (m Model) openForm(s *inventory.Server) (tea.Model, tea.Cmd) {
	id := ""
	if s != nil {
		id = s.ID
	}
	m.form = newForm(s, m.d.Inv.JumpOptions(id))
	m.screen = screenForm
	return m, textinput.Blink
}

func (m Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := &m.form
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		if cur := f.current(); isText(cur) {
			f.inputs[cur], cmd = f.inputs[cur].Update(msg)
		}
		return m, cmd
	}
	switch km.String() {
	case "esc":
		m.screen = screenList
		return m, nil
	case "tab", "down":
		return m, f.moveFocus(1)
	case "shift+tab", "up":
		return m, f.moveFocus(-1)
	case "enter":
		if f.focus == len(f.visible())-1 {
			return m.submitForm()
		}
		return m, f.moveFocus(1)
	}
	switch cur := f.current(); cur {
	case fAuth:
		if s := km.String(); s == " " || s == "left" || s == "right" {
			if f.auth == inventory.AuthKey {
				f.auth = inventory.AuthPassword
			} else {
				f.auth = inventory.AuthKey
			}
		}
	case fSudoMode:
		if s := km.String(); s == " " || s == "left" || s == "right" {
			step := 1
			if s == "left" {
				step = len(sudoModes) - 1
			}
			for i, mode := range sudoModes {
				if mode == f.sudoMode {
					f.sudoMode = sudoModes[(i+step)%len(sudoModes)]
					break
				}
			}
		}
	case fSavePw:
		if km.String() == " " {
			f.savePw = !f.savePw
		}
	case fJump:
		switch km.String() {
		case " ", "right":
			f.jump = (f.jump + 1) % len(f.jumpIDs)
		case "left":
			f.jump = (f.jump + len(f.jumpIDs) - 1) % len(f.jumpIDs)
		}
	default:
		var cmd tea.Cmd
		f.inputs[cur], cmd = f.inputs[cur].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) submitForm() (tea.Model, tea.Cmd) {
	f := &m.form
	s := f.orig
	s.Name = f.inputs[fName].Value()
	s.Host = f.inputs[fHost].Value()
	s.User = f.inputs[fUser].Value()
	s.Auth = f.auth
	s.Jump = f.jumpIDs[f.jump]
	s.KeyPath, s.SavePassword, s.SudoPassword = "", false, ""
	if f.auth == inventory.AuthKey {
		s.KeyPath = strings.TrimSpace(f.inputs[fKeyPath].Value())
		if f.sudoMode != inventory.SudoNone {
			s.SudoPassword = f.sudoMode
		}
	} else {
		s.SavePassword = f.savePw
	}
	s.Port = 0 // blank → inventory's default 22
	if p := strings.TrimSpace(f.inputs[fPort].Value()); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil {
			f.err = "port must be 1-65535"
			return m, nil
		}
		s.Port = port
	}
	var err error

	if f.editID == "" {
		s, err = m.d.Inv.Add(s)
	} else {
		err = m.d.Inv.Update(s)
	}
	if err != nil {
		f.err = err.Error()
		return m, nil
	}
	if err := m.d.Inv.Save(); err != nil {
		f.err = "could not save servers.yaml: " + err.Error()
		return m, nil
	}
	m.footer = ""
	if err := m.storeSecrets(s, f.inputs[fPassword].Value(), f.inputs[fSudoPw].Value()); err != nil {
		m.footer = "Saved, but the keychain failed: " + err.Error()
	}
	m.screen = screenList
	return m, func() tea.Msg { return serverSavedMsg{Server: s} }
}

// storeSecrets puts typed passwords where the server's settings say, and
// removes any secret the new settings no longer use.
func (m Model) storeSecrets(s inventory.Server, pw, sudoPw string) error {
	login, sudo := secrets.LoginAccount(s.ID), secrets.SudoAccount(s.ID)
	if s.Auth == inventory.AuthPassword {
		return errors.Join(m.placeSecret(login, pw, s.SavePassword), m.dropSecret(sudo))
	}
	err := m.dropSecret(login)
	switch s.SudoPassword {
	case inventory.SudoSaved:
		return errors.Join(err, m.placeSecret(sudo, sudoPw, true))
	case inventory.SudoAsk:
		return errors.Join(err, m.placeSecret(sudo, sudoPw, false))
	default:
		return errors.Join(err, m.dropSecret(sudo))
	}
}

// placeSecret keeps acct's secret in the keychain, or only in this session.
// A typed value wins; with nothing typed, the existing secret moves over.
func (m Model) placeSecret(acct, typed string, keychain bool) error {
	v := typed
	if v == "" {
		kv, err := m.d.Secrets.Get(acct)
		switch {
		case err == nil:
			v = kv
		case !errors.Is(err, secrets.ErrNotFound):
			return err
		default:
			v, _ = m.d.Session.Get(acct)
		}
	}
	if v != "" {
		m.d.Session.Set(acct, v)
	}
	if !keychain {
		return m.d.Secrets.Delete(acct)
	}
	if v == "" {
		return nil
	}
	return m.d.Secrets.Set(acct, v)
}

func (m Model) dropSecret(acct string) error {
	m.d.Session.Forget(acct)
	return m.d.Secrets.Delete(acct)
}

func (m Model) deleteServer(id string) (tea.Model, tea.Cmd) {
	m.confirmDelete = ""
	if err := m.d.Inv.Delete(id); err != nil {
		m.footer = err.Error()
		return m, nil
	}
	delete(m.selected, id)
	if err := m.d.Inv.Save(); err != nil {
		m.footer = "could not save servers.yaml: " + err.Error()
	}
	for _, acct := range []string{secrets.LoginAccount(id), secrets.SudoAccount(id)} {
		if err := m.d.Secrets.Delete(acct); err != nil {
			m.footer = "Deleted, but the keychain failed: " + err.Error()
		}
		m.d.Session.Forget(acct)
	}
	return m, func() tea.Msg { return serverDeletedMsg{ID: id} }
}

func (m Model) viewForm() string {
	f := m.form
	var b strings.Builder
	title := "Add server"
	if f.editID != "" {
		title = "Edit " + f.orig.Name
	}
	b.WriteString(" " + titleStyle.Render(title) + "\n\n")
	cur := f.current()
	for _, id := range f.visible() {
		marker := "  "
		if id == cur {
			marker = "› "
		}
		var val string
		switch id {
		case fAuth:
			val = choice([]string{"key", "password"}, string(f.auth))
		case fJump:
			val = choice(f.jumpNames, f.jumpNames[f.jump])
		case fSudoMode:
			names := make([]string, len(sudoModes))
			for i, s := range sudoModes {
				names[i] = string(s)
			}
			val = choice(names, string(f.sudoMode))
		case fSavePw:
			check := "[ ]"
			if f.savePw {
				check = "[x]"
			}
			val = check + " Save password to keychain"
		default:
			val = f.inputs[id].View()
		}
		fmt.Fprintf(&b, " %s%-14s %s\n", marker, fieldLabels[id], val)
	}
	b.WriteString("\n")
	if f.err != "" {
		b.WriteString(" " + errorStyle.Render(f.err) + "\n")
	}
	b.WriteString(" " + hintStyle.Render("tab/↓ next · shift+tab/↑ back · space toggle · enter on last field saves · esc cancel") + "\n")
	return b.String()
}

// choice renders options with the active one bracketed: "[key]  password".
func choice(opts []string, active string) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		if o == active {
			parts[i] = "[" + o + "]"
		} else {
			parts[i] = " " + o + " "
		}
	}
	return strings.Join(parts, " ")
}
