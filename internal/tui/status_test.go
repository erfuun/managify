package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

func TestStatusUpRow(t *testing.T) {
	d, env := liveDeps(t, 0)
	addLive(t, d, env, probeServer(t), "web-1", savedPassword|trusted)
	tm := start(t, d)
	waitFor(t, tm, "● up   load 0.4  RAM 41%  disk 63%")
	press(tm, "q")
	final(t, tm)
}

func TestStatusDownRow(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	d, _ := liveDeps(t, 0)
	if _, err := d.Inv.Add(inventory.Server{Name: "gone", Host: "127.0.0.1", Port: port, User: "root", Auth: inventory.AuthPassword}); err != nil {
		t.Fatal(err)
	}
	tm := start(t, d)
	// The cursor is on the down row, so the footer explains it.
	waitFor(t, tm, "○ down (refused)", "Can't reach 127.0.0.1:")
	press(tm, "q")
	final(t, tm)
}

func TestHostKeyPromptThenUp(t *testing.T) {
	d, env := liveDeps(t, 0)
	srv := probeServer(t)
	s := addLive(t, d, env, srv, "web-1", savedPassword)
	tm := start(t, d)
	waitFor(t, tm, "New server web-1 ("+s.Addr()+")", "Key fingerprint: SHA256:", "Trust and remember this key? (y/n)")
	press(tm, "y")
	waitFor(t, tm, "● up")
	press(tm, "q")
	final(t, tm)
}

func TestStaleStatusIgnoredAfterDelete(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := testkit.Start(t, sshtest.Options{Exec: func(ctx context.Context, _ string, _ io.Reader, _, _ io.Writer) int {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return 1
	}})
	d, env := liveDeps(t, 0)
	addLive(t, d, env, srv, "web-1", savedPassword|trusted)
	tm := start(t, d)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("probe never started")
	}
	press(tm, "d", "y")
	waitFor(t, tm, "No servers yet")
	// Deleting closes the connection, so the probe fails right away.
	time.Sleep(300 * time.Millisecond)
	press(tm, "q")
	m := final(t, tm)
	if len(m.status) != 0 || len(m.probing) != 0 {
		t.Fatalf("stale state kept: status=%v probing=%v", m.status, m.probing)
	}
}

func TestSaveProbesServer(t *testing.T) {
	d, env := liveDeps(t, 0)
	srv := probeServer(t)
	env.Trust(t, srv)
	tm := start(t, d)
	waitFor(t, tm, "No servers yet")
	press(tm, "a")
	typeText(tm, "web-1")
	press(tm, "tab")
	typeText(tm, srv.Host)
	press(tm, "tab")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlU}) // clear the default 22, in order with the typing
	typeText(tm, itoa(srv.Port))
	press(tm, "tab")
	typeText(tm, "root")
	press(tm, "tab", "space", "tab")
	typeText(tm, testkit.Password)
	press(tm, "tab", "enter")
	waitFor(t, tm, "● up")
	press(tm, "q")
	final(t, tm)
}

func TestNoPoolShowsUnknown(t *testing.T) {
	tm := start(t, testDeps(t, webServer))
	out := collect(tm, 300*time.Millisecond)
	if !bytes.Contains(out, []byte("? unknown")) || bytes.Contains(out, []byte("checking")) {
		t.Fatalf("output:\n%s", out)
	}
	press(tm, "q")
	final(t, tm)
}

// A probe started before an edit must not overwrite the edited server's status.
func TestProbeBeforeSaveIsSuperseded(t *testing.T) {
	d, env := liveDeps(t, 0)
	s := addLive(t, d, env, probeServer(t), "web-1", savedPassword|trusted)
	m := New(d)
	defer m.cancel()
	_ = m.probe(s) // the in-flight probe from launch
	m.forgetServer(s.ID)
	_ = m.retry(s) // what saving the form does
	m = m.applyStatus(statusMsg{ID: s.ID, Gen: 1, Err: errors.New("stale")})
	if _, ok := m.status[s.ID]; ok || !m.probing[s.ID] {
		t.Fatalf("stale result applied: status=%v probing=%v", m.status, m.probing)
	}
}
