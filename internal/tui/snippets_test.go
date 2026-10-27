package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/job"
	"github.com/erfuun/managify/internal/snippets"
)

func snipPath(t *testing.T, d Deps) string {
	return filepath.Join(filepath.Dir(invPath(t, d)), "snippets.yaml")
}

// addExampleSnippets adds the spec's two example snippets.
func addExampleSnippets(t *testing.T, d Deps) {
	t.Helper()
	for _, sn := range []snippets.Snippet{
		{Name: "deploy", Upload: &snippets.Upload{Local: `C:\work\deploy.sh`, Remote: "/tmp/deploy.sh"}, Command: "bash /tmp/deploy.sh", Sudo: true},
		{Name: "disk usage", Command: "df -h"},
	} {
		if err := d.Snippets.Add(sn); err != nil {
			t.Fatal(err)
		}
	}
}

func dfExec(_ context.Context, cmd string, _ io.Reader, stdout, _ io.Writer) int {
	if cmd != "df -h" {
		fmt.Fprintln(stdout, "unexpected: "+cmd)
		return 1
	}
	fmt.Fprintln(stdout, "Filesystem Size Used")
	return 0
}

func TestRunSnippet(t *testing.T) {
	d := runServers(t, 2, dfExec)
	addExampleSnippets(t, d)
	tm := start(t, d)
	waitFor(t, tm, "s-2")
	selectAll(tm, 2)
	press(tm, "s")
	waitFor(t, tm, "Snippets", "deploy", "bash /tmp/deploy.sh", "[sudo]", "[upload]", "disk usage", "df -h", "s-1, s-2")
	press(tm, "down", "enter")
	waitFor(t, tm, "Run: df -h", "s-1 ✓ 0", "s-2 ✓ 0")
	press(tm, "esc", "q")
	m := final(t, tm)
	for i := range 2 {
		if !strings.Contains(paneText(m, i), "Filesystem") {
			t.Errorf("pane %d = %q", i, paneText(m, i))
		}
	}
}

func TestSaveRunAsSnippet(t *testing.T) {
	d := runServers(t, 1, echoExec)
	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "r")
	typeText(tm, "uptime")
	press(tm, "enter")
	waitFor(t, tm, "s-1 ✓ 0", "ctrl+s save as snippet")
	press(tm, "ctrl+s")
	waitFor(t, tm, "Save as snippet", "uptime")
	press(tm, "u", "p") // as one paste, textinput reads "up" as its suggestion key
	press(tm, "ctrl+s")
	waitFor(t, tm, "Saved snippet up")
	press(tm, "esc", "q")
	m := final(t, tm)
	if m.screen != screenList {
		t.Fatalf("screen = %v", m.screen)
	}
	b, _ := os.ReadFile(snipPath(t, d))
	if !strings.Contains(string(b), "name: up") || !strings.Contains(string(b), "command: uptime") {
		t.Fatalf("snippets.yaml =\n%s", b)
	}
}

func TestSaveAsSnippetOnlyWhenSavable(t *testing.T) {
	cases := []struct {
		name    string
		spec    job.Spec
		running int
		hint    string
	}{
		{"still running", job.Spec{Command: "uptime"}, 1, "Wait for the run to finish, then ctrl+s."},
		{"download", job.Spec{Download: "/var/log/syslog"}, 0, "A download can't be saved as a snippet."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(testDeps(t))
			m.run = &runModel{spec: c.spec, running: c.running, cancel: func() {}}
			m.screen = screenRun
			got, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
			if gm := got.(Model); gm.screen != screenRun || gm.run.hint != c.hint {
				t.Fatalf("screen %v hint %q", gm.screen, gm.run.hint)
			}
		})
	}
}

func TestSaveUploadRunAsSnippet(t *testing.T) {
	m := New(testDeps(t))
	m.run = &runModel{spec: job.Spec{Upload: &job.Upload{Local: `C:\x\a.sh`, Remote: "/opt/"}, Command: "bash /opt/a.sh", Sudo: true}, cancel: func() {}}
	m.screen = screenRun
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	f := got.(Model).snipForm
	if got.(Model).screen != screenSnippetForm || f.inputs[sfLocal].Value() != `C:\x\a.sh` ||
		f.inputs[sfRemote].Value() != "/opt/" || f.inputs[sfCommand].Value() != "bash /opt/a.sh" || !f.sudo {
		t.Fatalf("form = %+v", f)
	}
}

func TestDeleteSnippet(t *testing.T) {
	d := testDeps(t)
	addExampleSnippets(t, d)
	tm := start(t, d)
	press(tm, "s")
	waitFor(t, tm, "deploy", "disk usage")
	press(tm, "d")
	waitFor(t, tm, "Delete deploy? (y/n)")
	press(tm, "y")
	press(tm, "esc", "q")
	m := final(t, tm)
	if len(m.d.Snippets.Snippets) != 1 || m.d.Snippets.Snippets[0].Name != "disk usage" {
		t.Fatalf("%+v", m.d.Snippets.Snippets)
	}
	b, _ := os.ReadFile(snipPath(t, d))
	if strings.Contains(string(b), "deploy") || !strings.Contains(string(b), "disk usage") {
		t.Fatalf("snippets.yaml =\n%s", b)
	}
}

func TestSnippetFormNewAndEdit(t *testing.T) {
	d := testDeps(t)
	tm := start(t, d)
	press(tm, "s")
	waitFor(t, tm, "No snippets yet")
	press(tm, "n")
	waitFor(t, tm, "New snippet")
	press(tm, "ctrl+s")
	waitFor(t, tm, "name is required")
	typeText(tm, "logs")
	press(tm, "tab", "tab", "tab")
	typeText(tm, "/tmp/x")
	press(tm, "tab")
	typeText(tm, "/var/log/")
	press(tm, "ctrl+s")
	waitFor(t, tm, "command is required")
	press(tm, "shift+tab", "shift+tab", "shift+tab")
	typeText(tm, "tail /var/log/syslog")
	press(tm, "enter", "enter", "enter", "enter") // enter on the last field saves
	waitFor(t, tm, "tail /var/log/syslog", "[upload]")
	press(tm, "e")
	waitFor(t, tm, "Edit logs")
	press(tm, "tab", "tab", " ")
	press(tm, "ctrl+s")
	waitFor(t, tm, "[sudo]")
	press(tm, "esc", "q")
	m := final(t, tm)
	got := m.d.Snippets.Snippets
	if len(got) != 1 || got[0].Command != "tail /var/log/syslog" || !got[0].Sudo ||
		got[0].Upload == nil || got[0].Upload.Local != "/tmp/x" || got[0].Upload.Remote != "/var/log/" {
		t.Fatalf("%+v", got)
	}
}

func TestSnippetUploadNeedsLocalFile(t *testing.T) {
	d := testDeps(t)
	tm := start(t, d)
	press(tm, "s", "n")
	waitFor(t, tm, "New snippet")
	typeText(tm, "x")
	press(tm, "tab")
	typeText(tm, "ls")
	press(tm, "tab", "tab", "tab")
	typeText(tm, "/tmp/")
	press(tm, "ctrl+s")
	waitFor(t, tm, "an upload needs a local file")
	press(tm, "esc", "esc", "q")
	if m := final(t, tm); len(m.d.Snippets.Snippets) != 0 {
		t.Fatalf("%+v", m.d.Snippets.Snippets)
	}
}
