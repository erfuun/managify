package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erfuun/managify/internal/job"
	"github.com/erfuun/managify/internal/remote"
)

func TestCleanText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\tb", "a       b"},
		{"abcdefgh\tx", "abcdefgh        x"},
		{"50%\r100%", "100%"},
		{"line\r", "line"},
		{"\x1b[2J\x1b]0;title\x07ok", "ok"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"bell\x07here", "bellhere"},
		{"c1\u009b2Jx", "c12Jx"},
		{"del\x7fx", "delx"},
		{"ünï\tcode", "ünï     code"},
	}
	for _, c := range cases {
		if got := cleanText(c.in); got != c.want {
			t.Errorf("cleanText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRunPaneSanitizesButLogKeepsRaw(t *testing.T) {
	m := New(testDeps(t))
	p := &pane{prog: -1}
	r := &runModel{byID: map[string]*pane{"s": p}, running: 1, cancel: func() {}}
	m.run = r
	m.applyRunBatch(runBatchMsg{run: r, events: []job.Event{
		{ServerID: "s", Kind: job.EvLine, Line: remote.Line{Text: "a\tb\x1b[2J"}},
		{ServerID: "s", Kind: job.EvNote, Text: "uploaded to /tmp/\x1b]0;x\x07f"},
	}})
	got := strings.Join(p.lines, "\n")
	if strings.ContainsAny(got, "\t\x1b\x07") || !strings.Contains(got, "a       b") || !strings.Contains(got, "/tmp/f") {
		t.Fatalf("pane = %q", got)
	}
}

// The log is written by job from the raw line; this guards that the TUI's
// cleanup never moves into the shared path.
func TestLogKeepsTabs(t *testing.T) {
	d := runServers(t, 1, func(_ context.Context, _ string, _ io.Reader, stdout, _ io.Writer) int {
		fmt.Fprint(stdout, "a\tb\x1b[2J\n")
		return 0
	})
	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "r")
	typeText(tm, "tabs")
	press(tm, "enter")
	waitFor(t, tm, "s-1 ✓ 0")
	press(tm, "esc", "q")
	m := final(t, tm)
	if got := paneText(m, 0); got != "a       b" {
		t.Fatalf("pane = %q", got)
	}
	logs, _ := filepath.Glob(filepath.Join(d.DataDir, "outputs", "s-1", "*.log"))
	if len(logs) != 1 {
		t.Fatalf("logs = %v", logs)
	}
	b, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(b), "a\tb\x1b[2J\n") {
		t.Fatalf("log =\n%q", b)
	}
}
