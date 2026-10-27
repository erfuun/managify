package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/muesli/cancelreader"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/erfuun/managify/internal/sshx"
)

// Shell is an interactive login shell on a PTY. It has the shape of
// tea.ExecCommand, so the TUI can hand the terminal over to it.
type Shell struct {
	c      sshx.Client
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func NewShell(c sshx.Client) *Shell {
	return &Shell{c: c, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
}

func (s *Shell) SetStdin(r io.Reader)  { s.stdin = r }
func (s *Shell) SetStdout(w io.Writer) { s.stdout = w }
func (s *Shell) SetStderr(w io.Writer) { s.stderr = w }

// Run lasts until the remote shell ends. The exit status of the user's last
// command is theirs, not an error.
func (s *Shell) Run() error {
	sess, err := s.c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	out, outIsTerm := terminal(s.stdout)
	w, h := 80, 24
	if outIsTerm {
		if tw, th, err := term.GetSize(int(out.Fd())); err == nil {
			w, h = tw, th
		}
		defer prepareOutput(out)()
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", h, w, modes); err != nil {
		return err
	}
	if in, ok := terminal(s.stdin); ok {
		old, err := term.MakeRaw(int(in.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(in.Fd()), old)
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	sess.Stdout, sess.Stderr = s.stdout, s.stderr
	if err := sess.Shell(); err != nil {
		return err
	}

	// A plain Read would outlive the session and swallow the first key
	// typed back in the TUI; a cancel reader can be stopped.
	cr, err := cancelreader.NewReader(s.stdin)
	if err != nil {
		return err
	}
	defer cr.Close() // on Windows this also restores the console input mode
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		io.Copy(stdin, cr)
		stdin.Close()
	}()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if outIsTerm {
		go watchResize(ctx, int(out.Fd()), func(w, h int) { sess.WindowChange(h, w) })
	}

	err = sess.Wait()
	if cr.Cancel() {
		select { // bounded: never leave the user stuck in a finished shell
		case <-copied:
		case <-time.After(time.Second):
		}
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}

func terminal(v any) (*os.File, bool) {
	f, ok := v.(*os.File)
	return f, ok && term.IsTerminal(int(f.Fd()))
}
