package tui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/remote"
	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

// runExec answers status probes like probeExec and hands every other
// command to f.
func runExec(f sshtest.ExecFunc) sshtest.ExecFunc {
	return func(ctx context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) int {
		if cmd == remote.ProbeCommand {
			return probeExec(ctx, cmd, stdin, stdout, stderr)
		}
		return f(ctx, cmd, stdin, stdout, stderr)
	}
}

func echoExec(_ context.Context, cmd string, _ io.Reader, stdout, _ io.Writer) int {
	fmt.Fprintln(stdout, strings.TrimPrefix(cmd, "echo "))
	return 0
}

// runServers adds n trusted, saved-password servers named s-1…s-n, all running f.
func runServers(t *testing.T, n int, f sshtest.ExecFunc) Deps {
	t.Helper()
	d, env := liveDeps(t, 0)
	for i := 1; i <= n; i++ {
		srv := testkit.Start(t, sshtest.Options{Exec: runExec(f)})
		addLive(t, d, env, srv, fmt.Sprintf("s-%d", i), savedPassword|trusted)
	}
	return d
}

// selectAll selects every row from the top.
func selectAll(tm interface{ Send(tea.Msg) }, n int) {
	for i := 0; i < n; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
}

func paneText(m Model, i int) string { return strings.Join(m.run.panes[i].lines, "\n") }

func TestRunTwoServers(t *testing.T) {
	d := runServers(t, 2, echoExec)
	tm := start(t, d)
	waitFor(t, tm, "s-2")
	selectAll(tm, 2)
	press(tm, "r")
	waitFor(t, tm, "Run on s-1, s-2", "[ ] sudo")
	typeText(tm, "echo hi")
	press(tm, "enter")
	waitFor(t, tm, "s-1 ✓ 0", "s-2 ✓ 0", "logs: "+filepath.Join(d.DataDir, "outputs"))
	press(tm, "esc")
	waitFor(t, tm, "space select")
	press(tm, "q")
	m := final(t, tm)
	for i := range 2 {
		if !strings.Contains(paneText(m, i), "hi") {
			t.Errorf("pane %d = %q", i, paneText(m, i))
		}
	}
}

func TestRunEmptyCommandIgnored(t *testing.T) {
	d := runServers(t, 1, echoExec)
	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "r")
	waitFor(t, tm, "Run on s-1")
	press(tm, "enter", "esc")
	waitFor(t, tm, "space select")
	press(tm, "q")
	if m := final(t, tm); m.run != nil {
		t.Fatal("empty command started a run")
	}
}

func TestRunSudoToggle(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	d := runServers(t, 1, func(_ context.Context, cmd string, stdin io.Reader, stdout, _ io.Writer) int {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if line != testkit.Password+"\n" {
			return 1
		}
		fmt.Fprintln(stdout, "root")
		return 0
	})
	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "r", "ctrl+o")
	waitFor(t, tm, "[x] sudo")
	typeText(tm, "whoami")
	press(tm, "enter")
	waitFor(t, tm, "s-1 ✓ 0")
	press(tm, "esc", "q")
	final(t, tm)
	mu.Lock()
	defer mu.Unlock()
	if len(cmds) != 1 || !strings.HasPrefix(cmds[0], "sudo -S -p '' -- sh -c ") {
		t.Fatalf("commands = %q", cmds)
	}
}

func TestRunCancel(t *testing.T) {
	d := runServers(t, 1, func(ctx context.Context, _ string, _ io.Reader, stdout, _ io.Writer) int {
		fmt.Fprintln(stdout, "started")
		<-ctx.Done()
		return 130
	})
	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "r")
	typeText(tm, "sleep 100")
	press(tm, "enter")
	waitFor(t, tm, "s-1 · running…", "started")
	press(tm, "esc")
	waitFor(t, tm, "ctrl+c to cancel")
	press(tm, "ctrl+c")
	waitFor(t, tm, "s-1 ✗ cancelled locally; process may still be running on s-1")
	press(tm, "esc")
	waitFor(t, tm, "space select")
	press(tm, "q")
	final(t, tm)
}

func TestRunFailureTitles(t *testing.T) {
	d := runServers(t, 1, func(context.Context, string, io.Reader, io.Writer, io.Writer) int { return 127 })
	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "r")
	typeText(tm, "nope")
	press(tm, "enter")
	waitFor(t, tm, "s-1 ✗ 127")
	press(tm, "esc", "q")
	final(t, tm)
}

func TestRunTabsOverThree(t *testing.T) {
	d := runServers(t, 4, echoExec)
	tm := start(t, d)
	waitFor(t, tm, "s-4")
	selectAll(tm, 4)
	press(tm, "r")
	typeText(tm, "echo hi")
	press(tm, "enter")
	waitFor(t, tm, "[s-1]", "s-4")
	press(tm, "tab")
	waitFor(t, tm, "[s-2]")
	press(tm, "shift+tab", "shift+tab")
	waitFor(t, tm, "[s-4]")
	press(tm, "q") // not a quit key here
	press(tm, "esc", "q")
	if m := final(t, tm); m.run.focus != 3 {
		t.Fatalf("focus = %d", m.run.focus)
	}
}

func TestRunFloodDoesNotStall(t *testing.T) {
	const n = 100_000
	d := runServers(t, 1, func(_ context.Context, _ string, _ io.Reader, stdout, _ io.Writer) int {
		w := bufio.NewWriter(stdout)
		for i := range n {
			fmt.Fprintf(w, "line %d\n", i)
		}
		w.Flush()
		return 0
	})
	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "r")
	typeText(tm, "flood")
	press(tm, "enter")
	waitForWithin(t, tm, 10*time.Second, "s-1 ✓ 0")
	press(tm, "esc", "q")
	m := final(t, tm)
	if got := len(m.run.panes[0].lines); got > maxPaneLines+1000 {
		t.Fatalf("pane keeps %d lines", got)
	}
	logs, _ := filepath.Glob(filepath.Join(d.DataDir, "outputs", "s-1", "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs = %v", logs)
	}
	f, err := os.Open(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	count := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "line ") {
			count++
		}
	}
	if count != n {
		t.Fatalf("log has %d output lines, want %d", count, n)
	}
}

// A run whose prompt is cancelled leaves the server marked, so background
// probes don't ask again.
func TestRunCancelledPromptNotRepeatedByProbe(t *testing.T) {
	d, env := liveDeps(t, 50*time.Millisecond)
	srv := testkit.Start(t, sshtest.Options{Exec: runExec(echoExec)})
	addLive(t, d, env, srv, "s-1", trusted)
	tm := start(t, d)
	waitFor(t, tm, "Password for")
	press(tm, "esc")
	waitFor(t, tm, "○ down (cancelled)")
	press(tm, "r")
	typeText(tm, "echo hi")
	press(tm, "enter")
	waitFor(t, tm, "Password for")
	press(tm, "esc")
	waitFor(t, tm, "s-1 ✗ Cancelled.")
	if out := collect(tm, 300*time.Millisecond); bytes.Contains(out, []byte("Password for")) {
		t.Fatalf("background probe asked again:\n%s", out)
	}
	press(tm, "esc", "q")
	final(t, tm)
}

func TestPaneScrolling(t *testing.T) {
	p := &pane{vp: viewport.New(40, 5)}
	for i := range 50 {
		p.add(fmt.Sprintf("line %d", i))
	}
	p.refresh()
	if !p.vp.AtBottom() {
		t.Fatal("new output not followed")
	}
	m := New(testDeps(t))
	defer m.cancel()
	m.run = &runModel{panes: []*pane{p}, byID: map[string]*pane{}}
	m.updateRun(tea.KeyMsg{Type: tea.KeyPgUp})
	off := p.vp.YOffset
	if p.vp.AtBottom() {
		t.Fatal("pgup did not scroll")
	}
	p.add("more")
	p.refresh()
	if p.vp.YOffset != off {
		t.Fatalf("scrolled-up pane jumped: %d → %d", off, p.vp.YOffset)
	}
	m.updateRun(tea.KeyMsg{Type: tea.KeyPgDown})
	m.updateRun(tea.KeyMsg{Type: tea.KeyPgDown})
	p.add("again")
	p.refresh()
	if !p.vp.AtBottom() {
		t.Fatal("pane at bottom stopped following")
	}
}

// The run view fills the terminal; a prompt above it must still be seen.
func TestPromptVisibleOnRunScreen(t *testing.T) {
	d, env := liveDeps(t, 0)
	srv := testkit.Start(t, sshtest.Options{Exec: runExec(echoExec)})
	addLive(t, d, env, srv, "s-1", trusted)
	tm := start(t, d)
	waitFor(t, tm, "Password for")
	press(tm, "esc")
	waitFor(t, tm, "○ down (cancelled)")
	press(tm, "r")
	typeText(tm, "echo hi")
	press(tm, "enter")
	waitFor(t, tm, "Password for root@", "s-1 · running…")
	typeText(tm, testkit.Password)
	press(tm, "enter")
	waitFor(t, tm, "s-1 ✓ 0")
	press(tm, "esc", "q")
	final(t, tm)
}

func TestViewNeverTallerThanTerminal(t *testing.T) {
	m := New(testDeps(t))
	defer m.cancel()
	m.height = 5
	m.footer = "x"
	for range 10 {
		m.d.Inv.Servers = append(m.d.Inv.Servers, webServer)
	}
	if lines := strings.Count(m.View(), "\n") + 1; lines > 5 {
		t.Fatalf("view has %d lines for a 5-line terminal", lines)
	}
}
