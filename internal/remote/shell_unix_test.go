//go:build !windows

package remote

import (
	"os"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

// After the shell ends, no leftover goroutine may still be reading stdin:
// it would steal the first key typed back in the TUI.
func TestShellReleasesStdin(t *testing.T) {
	srv := testkit.Start(t, sshtest.Options{Shell: true})
	sh := NewShell(testkit.Dial(t, srv))
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	sh.SetStdin(r)
	sh.SetStdout(new(discard))
	sh.SetStderr(new(discard))
	done := make(chan error, 1)
	go func() { done <- sh.Run() }()
	time.Sleep(200 * time.Millisecond)
	srv.DropAll()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after the session ended")
	}

	w.Write([]byte("k"))
	got := make(chan byte, 1)
	go func() {
		b := make([]byte, 1)
		if n, _ := r.Read(b); n == 1 {
			got <- b[0]
		}
	}()
	select {
	case b := <-got:
		if b != 'k' {
			t.Fatalf("read %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("the key was consumed by a leftover reader")
	}
}

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }
