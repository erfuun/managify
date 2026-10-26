package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/muesli/termenv"

	"managify/internal/inventory"
	"managify/internal/secrets"
)

func TestMain(m *testing.M) {
	// Plain output so assertions never straddle ANSI escapes.
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}

var (
	webServer = inventory.Server{Name: "web-1", Host: "203.0.113.10", User: "root", Auth: inventory.AuthKey}
	dbServer  = inventory.Server{Name: "db-1", Host: "203.0.113.20", User: "admin", Auth: inventory.AuthPassword, SavePassword: true}
)

// invPaths remembers where each test inventory lives (the path is unexported).
var invPaths = map[*inventory.Inventory]string{}

func invPath(t *testing.T, d Deps) string {
	t.Helper()
	p, ok := invPaths[d.Inv]
	if !ok {
		t.Fatal("inventory not created by testDeps")
	}
	return p
}

func testDeps(t *testing.T, servers ...inventory.Server) Deps {
	t.Helper()
	p := filepath.Join(t.TempDir(), "servers.yaml")
	inv, err := inventory.Load(p)
	invPaths[inv] = p
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range servers {
		if _, err := inv.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	return Deps{
		Inv:     inv,
		Secrets: secrets.NewMem(),
		Session: secrets.NewSession(),
		DataDir: t.TempDir(),
		Now:     time.Now,
	}
}

func start(t *testing.T, d Deps) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, New(d), teatest.WithInitialTermSize(120, 30))
}

// press sends one key: "enter", "esc", "tab", "shift+tab", "space", "up", "down",
// "ctrl+c", "ctrl+o", "ctrl+s", or literal runes.
func press(tm *teatest.TestModel, keys ...string) {
	for _, k := range keys {
		var m tea.KeyMsg
		switch k {
		case "enter":
			m = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			m = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			m = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			m = tea.KeyMsg{Type: tea.KeyShiftTab}
		case "space":
			m = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		case "up":
			m = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			m = tea.KeyMsg{Type: tea.KeyDown}
		case "ctrl+c":
			m = tea.KeyMsg{Type: tea.KeyCtrlC}
		case "ctrl+o":
			m = tea.KeyMsg{Type: tea.KeyCtrlO}
		case "ctrl+s":
			m = tea.KeyMsg{Type: tea.KeyCtrlS}
		default:
			m = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		tm.Send(m)
	}
}

// keyBackspaces is a batch of n backspaces sent as one message sequence.
func keyBackspaces(n int) tea.Msg {
	cmds := make([]tea.Cmd, n)
	for i := range cmds {
		cmds[i] = func() tea.Msg { return tea.KeyMsg{Type: tea.KeyBackspace} }
	}
	return tea.Sequence(cmds...)()
}

// typeText sends s as runes, one message per call like a paste.
func typeText(tm *teatest.TestModel, s string) {
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func waitFor(t *testing.T, tm *teatest.TestModel, subs ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, s := range subs {
			if !bytes.Contains(b, []byte(s)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
}

func final(t *testing.T, tm *teatest.TestModel) Model {
	t.Helper()
	return tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(Model)
}
