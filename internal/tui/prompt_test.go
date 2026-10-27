package tui

import (
	"bytes"
	"strconv"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

func itoa(n int) string { return strconv.Itoa(n) }

func TestPasswordPromptMasked(t *testing.T) {
	d, env := liveDeps(t, 0)
	srv := probeServer(t)
	addLive(t, d, env, srv, "web-1", trusted)
	tm := start(t, d)
	waitFor(t, tm, "Password for root@"+srv.Host+" (web-1):")
	typeText(tm, testkit.Password)
	press(tm, "enter")
	waitForWithout(t, tm, testkit.Password, "● up")
	press(tm, "q")
	final(t, tm)
}

// List keys typed into a prompt are password characters, not commands.
func TestPromptSwallowsListKeys(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "qdy", Exec: probeExec})
	d, env := liveDeps(t, 0)
	addLive(t, d, env, srv, "web-1", trusted)
	tm := start(t, d)
	waitFor(t, tm, "Password for root@")
	press(tm, "q", "d", "y", "enter")
	waitFor(t, tm, "● up")
	press(tm, "q")
	if m := final(t, tm); len(m.d.Inv.Servers) != 1 {
		t.Fatal("a typed d+y deleted the server")
	}
}

func TestTwoPromptsOneAtATime(t *testing.T) {
	d, env := liveDeps(t, 0)
	srvA, srvB := probeServer(t), probeServer(t)
	addLive(t, d, env, srvA, "a-1", trusted)
	addLive(t, d, env, srvB, "b-1", trusted)
	tm := start(t, d)
	var out []byte
	for deadline := time.Now().Add(5 * time.Second); ; {
		out = append(out, collect(tm, 50*time.Millisecond)...)
		if bytes.Contains(out, []byte("(a-1):")) || bytes.Contains(out, []byte("(b-1):")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no prompt:\n%s", out)
		}
	}
	out = append(out, collect(tm, 300*time.Millisecond)...)
	first, second := "(a-1):", "(b-1):"
	if bytes.Contains(out, []byte(second)) {
		first, second = second, first
	}
	if bytes.Contains(out, []byte(second)) {
		t.Fatalf("both prompts shown at once:\n%s", out)
	}
	typeText(tm, testkit.Password)
	press(tm, "enter")
	waitFor(t, tm, "Password for root@", second)
	typeText(tm, testkit.Password)
	press(tm, "enter")
	waitForRe(t, tm, `a-1 +\S+ +● up`, `b-1 +\S+ +● up`)
	press(tm, "q")
	final(t, tm)
}

func TestCancelledPromptNotRepeatedByBackgroundProbe(t *testing.T) {
	d, env := liveDeps(t, 50*time.Millisecond)
	addLive(t, d, env, probeServer(t), "web-1", trusted)
	tm := start(t, d)
	waitFor(t, tm, "Password for root@")
	press(tm, "esc")
	waitFor(t, tm, "○ down (cancelled)")
	if out := collect(tm, 300*time.Millisecond); bytes.Contains(out, []byte("Password for")) {
		t.Fatalf("background probe asked again:\n%s", out)
	}
	press(tm, "R")
	waitFor(t, tm, "Password for root@")
	press(tm, "esc", "q")
	final(t, tm)
}
