package remote

import (
	"bytes"
	"strings"
	"testing"

	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

func TestShellEchoAndExit(t *testing.T) {
	srv := testkit.Start(t, sshtest.Options{Shell: true})
	sh := NewShell(testkit.Dial(t, srv))
	var out, errOut bytes.Buffer // separate: two copiers into one Buffer race
	sh.SetStdin(strings.NewReader("echo hi\nexit\n"))
	sh.SetStdout(&out)
	sh.SetStderr(&errOut)
	if err := sh.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hi") {
		t.Fatalf("output = %q", out.String())
	}
	if srv.PtyTerm() != "xterm-256color" {
		t.Fatalf("term = %q", srv.PtyTerm())
	}
	if sizes := srv.WindowSizes(); len(sizes) == 0 || sizes[0] != [2]int{80, 24} {
		t.Fatalf("sizes = %v", sizes)
	}
}
