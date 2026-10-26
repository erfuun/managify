package remote

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/erfuun/managify/internal/sshx"
)

type Stream int

const (
	Stdout Stream = iota
	Stderr
)

// Line is one line of output, without its "\n" or "\r\n".
type Line struct {
	Stream Stream
	Text   string
}

// RunOpts.Sudo with an empty SudoPassword runs "sudo -n".
type RunOpts struct {
	Sudo         bool
	SudoPassword string
}

type Result struct {
	ExitCode  int // -1 when there is none: cancelled, or the server sent no status
	Duration  time.Duration
	Cancelled bool
	Err       error
}

// cancelGrace is how long a cancelled command gets to exit after SIGINT.
const cancelGrace = 2 * time.Second

// Run runs cmd and hands each output line to sink, one call at a time. It
// returns only after every line has been delivered.
func Run(ctx context.Context, c sshx.Client, cmd string, opts RunOpts, sink func(Line)) Result {
	start := time.Now()
	result := func(r Result) Result {
		r.Duration = time.Since(start)
		return r
	}
	sess, err := openSession(ctx, c)
	if err != nil {
		if ctx.Err() != nil {
			return result(Result{ExitCode: -1, Cancelled: true})
		}
		return result(Result{ExitCode: -1, Err: err})
	}
	defer sess.Close()

	stdin, err := sess.StdinPipe()
	if err != nil {
		return result(Result{ExitCode: -1, Err: err})
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return result(Result{ExitCode: -1, Err: err})
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return result(Result{ExitCode: -1, Err: err})
	}
	if err := sess.Start(cmd); err != nil {
		return result(Result{ExitCode: -1, Err: err})
	}
	stdin.Close()

	var mu sync.Mutex
	var readers sync.WaitGroup
	for stream, r := range map[Stream]io.Reader{Stdout: stdout, Stderr: stderr} {
		readers.Add(1)
		go func() {
			defer readers.Done()
			readLines(r, func(text string) {
				mu.Lock()
				defer mu.Unlock()
				sink(Line{Stream: stream, Text: text})
			})
		}()
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		// Best effort: the server may not deliver signals, so close after a grace period.
		sess.Signal(ssh.SIGINT)
		select {
		case <-done:
		case <-time.After(cancelGrace):
		}
		sess.Close()
		readers.Wait()
		return result(Result{ExitCode: -1, Cancelled: true})
	}
	readers.Wait()

	var exit *ssh.ExitError
	switch {
	case err == nil:
		return result(Result{})
	case errors.As(err, &exit):
		return result(Result{ExitCode: exit.ExitStatus()})
	}
	// Includes *ssh.ExitMissingError: the command ended without a status.
	return result(Result{ExitCode: -1, Err: err})
}

// readLines emits every line of r, including a final one with no newline.
func readLines(r io.Reader, emit func(string)) {
	br := bufio.NewReader(r)
	for {
		s, err := br.ReadString('\n')
		if s != "" {
			s = strings.TrimSuffix(s, "\n")
			emit(strings.TrimSuffix(s, "\r"))
		}
		if err != nil {
			return
		}
	}
}
