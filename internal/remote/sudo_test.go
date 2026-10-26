package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

func TestBuildCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		opts RunOpts
		want string
	}{
		{"echo hi", RunOpts{}, "echo hi"},
		{"echo 'hi'", RunOpts{Sudo: true, SudoPassword: "pw"}, `sudo -S -p '' -- sh -c 'exec </dev/null; echo '\''hi'\'''`},
		{"apt update && apt upgrade -y", RunOpts{Sudo: true}, `sudo -n -- sh -c 'exec </dev/null; apt update && apt upgrade -y'`},
	}
	for _, c := range cases {
		if got := BuildCommand(c.cmd, c.opts); got != c.want {
			t.Errorf("BuildCommand(%q) =\n %s\nwant\n %s", c.cmd, got, c.want)
		}
	}
}

const sudoPw = "s3cret"

var withPw = RunOpts{Sudo: true, SudoPassword: sudoPw}

// sudoRun runs "whoami" with sudo against a fake sudo that fails with stderr
// (when non-empty) after checking stdin: the password line with -S, nothing with -n.
func sudoRun(t *testing.T, opts RunOpts, stderr string) (Result, []Line) {
	t.Helper()
	prefix, wantIn := "sudo -n -- sh -c ", ""
	if opts.SudoPassword != "" {
		prefix, wantIn = "sudo -S -p '' -- sh -c ", sudoPw+"\n"
	}
	srv := testkit.Start(t, sshtest.Options{Exec: func(_ context.Context, cmd string, stdin io.Reader, stdout, errw io.Writer) int {
		if strings.Contains(cmd, sudoPw) {
			fmt.Fprintln(stdout, "password in command line")
			return 99
		}
		if !strings.HasPrefix(cmd, prefix) {
			fmt.Fprintf(stdout, "unexpected command %q", cmd)
			return 127
		}
		in, _ := io.ReadAll(stdin) // Run closes stdin right after the password
		if string(in) != wantIn {
			fmt.Fprintf(stdout, "bad stdin %q", in)
			return 98
		}
		if stderr != "" {
			fmt.Fprintln(errw, stderr)
			return 1
		}
		fmt.Fprintln(stdout, "ok")
		return 0
	}})
	var l lines
	res := Run(context.Background(), testkit.Dial(t, srv), "whoami", opts, l.sink)
	for _, ln := range l.got {
		if strings.Contains(ln.Text, sudoPw) {
			t.Fatalf("password echoed in output: %+v", ln)
		}
	}
	return res, l.got
}

func TestSudoFeedsPasswordOnStdin(t *testing.T) {
	res, got := sudoRun(t, withPw, "")
	if res.ExitCode != 0 || res.Err != nil || len(got) != 1 || got[0] != (Line{Stdout, "ok"}) {
		t.Fatalf("res = %+v, lines = %+v", res, got)
	}
}

func TestSudoWrongPassword(t *testing.T) {
	res, _ := sudoRun(t, withPw, "sudo: 1 incorrect password attempt")
	if !errors.Is(res.Err, ErrSudoRejected) || res.ExitCode != 1 {
		t.Fatalf("res = %+v", res)
	}
}

// Mode none: sudo -n, and no password is written.
func TestSudoNeedsPassword(t *testing.T) {
	res, _ := sudoRun(t, RunOpts{Sudo: true}, "sudo: a password is required")
	if !errors.Is(res.Err, ErrSudoPasswordRequired) {
		t.Fatalf("res = %+v", res)
	}
}

func TestSudoNeedsTTY(t *testing.T) {
	for _, msg := range []string{"sudo: sorry, you must have a tty to run sudo", "sudo: a terminal is required to read the password"} {
		if res, _ := sudoRun(t, withPw, msg); !errors.Is(res.Err, ErrSudoNeedsTTY) {
			t.Errorf("%q: res = %+v", msg, res)
		}
	}
}

// A command's own failure is not a sudo error, even if it mentions one.
func TestSudoErrorOnlyOnFailure(t *testing.T) {
	srv := testkit.Start(t, sshtest.Options{Exec: func(_ context.Context, _ string, _ io.Reader, _, errw io.Writer) int {
		fmt.Fprintln(errw, "warning: a password is required")
		return 0
	}})
	res := Run(context.Background(), testkit.Dial(t, srv), "x", RunOpts{Sudo: true}, func(Line) {})
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("res = %+v", res)
	}
}
