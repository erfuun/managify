package remote

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

type lines struct {
	mu  sync.Mutex
	got []Line
}

func (l *lines) sink(ln Line) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, ln)
}

func runOn(t *testing.T, exec sshtest.ExecFunc) (*sshtest.Server, func(context.Context) (Result, []Line)) {
	srv := testkit.Start(t, sshtest.Options{Exec: exec})
	c := testkit.Dial(t, srv)
	return srv, func(ctx context.Context) (Result, []Line) {
		var l lines
		res := Run(ctx, c, "cmd", RunOpts{}, l.sink)
		return res, l.got
	}
}

func TestRunStreamsBothStreams(t *testing.T) {
	_, run := runOn(t, func(_ context.Context, _ string, _ io.Reader, stdout, stderr io.Writer) int {
		io.WriteString(stdout, "a\n")
		io.WriteString(stderr, "b\n")
		io.WriteString(stdout, "c")
		return 0
	})
	res, got := run(context.Background())
	for _, want := range []Line{{Stdout, "a"}, {Stderr, "b"}, {Stdout, "c"}} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %+v in %+v", want, got)
		}
	}
	if len(got) != 3 || res.ExitCode != 0 || res.Err != nil || res.Cancelled {
		t.Fatalf("res = %+v, lines = %+v", res, got)
	}
}

func TestRunExitCode(t *testing.T) {
	_, run := runOn(t, func(context.Context, string, io.Reader, io.Writer, io.Writer) int { return 127 })
	res, _ := run(context.Background())
	if res.ExitCode != 127 || res.Err != nil {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunLongLine(t *testing.T) {
	long := strings.Repeat("x", 200*1024)
	_, run := runOn(t, func(_ context.Context, _ string, _ io.Reader, stdout, _ io.Writer) int {
		io.WriteString(stdout, long+"\n")
		return 0
	})
	_, got := run(context.Background())
	if len(got) != 1 || got[0].Text != long {
		t.Fatalf("got %d lines", len(got))
	}
}

func TestRunCRLFStripped(t *testing.T) {
	_, run := runOn(t, func(_ context.Context, _ string, _ io.Reader, stdout, _ io.Writer) int {
		io.WriteString(stdout, "x\r\n")
		return 0
	})
	_, got := run(context.Background())
	if len(got) != 1 || got[0] != (Line{Stdout, "x"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestRunCancelSendsINT(t *testing.T) {
	srv, run := runOn(t, func(ctx context.Context, _ string, _ io.Reader, _, _ io.Writer) int {
		<-ctx.Done()
		return 130
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, _ := run(ctx)
	if !res.Cancelled || res.ExitCode != -1 || time.Since(start) > time.Second {
		t.Fatalf("res = %+v after %v", res, time.Since(start))
	}
	if !slices.Contains(srv.Signals(), "INT") {
		t.Fatalf("signals = %v", srv.Signals())
	}
}

func TestRunCancelIgnoredSignalStillReturns(t *testing.T) {
	_, run := runOn(t, func(context.Context, string, io.Reader, io.Writer, io.Writer) int {
		time.Sleep(10 * time.Second)
		return 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, _ := run(ctx)
	if !res.Cancelled || time.Since(start) > 3*time.Second {
		t.Fatalf("res = %+v after %v", res, time.Since(start))
	}
}
