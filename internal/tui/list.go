package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/erfuun/managify/internal/inventory"
)

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	n := len(m.d.Inv.Servers)
	m.cursor = clamp(m.cursor, 0, max(n-1, 0))
	if m.confirmDelete != "" {
		if km.String() == "y" {
			return m.deleteServer(m.confirmDelete)
		}
		m.confirmDelete = ""
		return m, nil
	}
	if m.showHelp {
		switch {
		case key.Matches(km, keys.Quit):
			return m.quit()
		case key.Matches(km, keys.Help), km.Type == tea.KeyEsc:
			m.showHelp = false
		}
		return m, nil
	}
	switch {
	case key.Matches(km, keys.Quit):
		return m.quit()
	case key.Matches(km, keys.Help):
		m.showHelp = true
	case key.Matches(km, keys.Refresh):
		// Explicit, so it clears every needs-action mark: locked servers ask again.
		for id := range m.needsAction {
			delete(m.needsAction, id)
		}
		return m, m.probeAll()
	case key.Matches(km, keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(km, keys.Down):
		if m.cursor < n-1 {
			m.cursor++
		}
	case key.Matches(km, keys.Run):
		if t := m.Targets(); len(t) > 0 && m.d.Pool != nil {
			return m.openCmdInput(t)
		}
	case key.Matches(km, keys.Add):
		return m.openForm(nil)
	// Edit and delete act on the cursor row, never the whole selection.
	case key.Matches(km, keys.Edit):
		if s, ok := m.cursorServer(); ok {
			return m.openForm(&s)
		}
	case key.Matches(km, keys.Delete):
		if s, ok := m.cursorServer(); ok {
			m.confirmDelete = s.ID
		}
	case key.Matches(km, keys.Select):
		if s, ok := m.cursorServer(); ok {
			if m.selected[s.ID] {
				delete(m.selected, s.ID)
			} else {
				m.selected[s.ID] = true
			}
		}
	}
	return m, nil
}

func (m Model) viewList() string {
	w := m.viewWidth()
	var b strings.Builder

	title := titleStyle.Render("Managify")
	actions := hintStyle.Render("[a]dd [e]dit [d]el  [?]help")
	gap := max(w-lipgloss.Width(title)-lipgloss.Width(actions)-2, 1)
	b.WriteString(" " + title + strings.Repeat(" ", gap) + actions + "\n")
	rule := " " + strings.Repeat("─", max(w-2, 1)) + "\n"
	b.WriteString(rule)

	servers := m.d.Inv.Servers
	if len(servers) == 0 {
		b.WriteString(" No servers yet. Press a to add one.\n")
	}
	nameW, addrW := 8, 16
	for _, s := range servers {
		nameW = max(nameW, lipgloss.Width(s.Name))
		addrW = max(addrW, lipgloss.Width(address(s)))
	}
	cursor := clamp(m.cursor, 0, max(len(servers)-1, 0))
	for i, s := range servers {
		check := "[ ]"
		if m.selected[s.ID] {
			check = "[x]"
		}
		// The cursor row is reversed as a whole; inner colors would end it early.
		row := fmt.Sprintf("%s %-*s %-*s  %s", check, nameW, s.Name, addrW, address(s), m.statusCell(s, i != cursor))
		if i == cursor {
			row = cursorStyle.Render(row)
		}
		b.WriteString(" " + row + "\n")
	}

	b.WriteString(rule)
	if s, ok := m.d.Inv.Get(m.confirmDelete); ok {
		b.WriteString(" " + errorStyle.Render(fmt.Sprintf("Delete %s? (y/n)", s.Name)) + "\n")
	} else if m.footer != "" {
		b.WriteString(" " + errorStyle.Render(m.footer) + "\n")
	} else if s, ok := m.cursorServer(); ok && m.downText(s) != "" {
		b.WriteString(" " + errorStyle.Render(m.downText(s)) + "\n")
	}
	b.WriteString(" " + hintStyle.Render("space select · enter shell · r run · u upload · g download · s snippets") + "\n")
	return b.String()
}

func address(s inventory.Server) string {
	a := s.User + "@" + s.Host
	if s.Port != 22 && s.Port != 0 {
		a += ":" + strconv.Itoa(s.Port)
	}
	return a
}

func (m Model) viewHelp() string {
	var b strings.Builder
	b.WriteString(" " + titleStyle.Render("Keys") + "\n\n")
	for _, k := range keys.all() {
		h := k.Help()
		fmt.Fprintf(&b, "   %-8s %s\n", h.Key, h.Desc)
	}
	b.WriteString("\n " + hintStyle.Render("Press ? or esc to close") + "\n")
	return b.String()
}
