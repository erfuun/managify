package tui

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/inventory"
)

func TestShellDialErrorShownInFooter(t *testing.T) {
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
	waitFor(t, tm, "○ down (refused)")
	press(tm, "enter")
	collect(tm, 500*time.Millisecond) // a refused dial fails at once
	press(tm, "q")
	// The footer, not just the status line, carries the shell's own error.
	if m := final(t, tm); !strings.Contains(m.footer, "Can't reach 127.0.0.1:") {
		t.Fatalf("footer = %q", m.footer)
	}
}
