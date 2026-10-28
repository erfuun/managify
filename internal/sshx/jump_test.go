package sshx_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/sshx"
	"github.com/erfuun/managify/internal/testkit"
)

func echoExec(_ context.Context, cmd string, _ io.Reader, stdout, _ io.Writer) int {
	fmt.Fprint(stdout, strings.TrimPrefix(cmd, "echo "))
	return 0
}

type jumpSetup struct {
	env          *testkit.Env
	jsrv, tsrv   *sshtest.Server
	jump, target inventory.Server
}

// newJump starts web-1 (a jump server with jopts) and db-1 behind it, both
// trusted with saved passwords, and adds them to env.Inv.
func newJump(t *testing.T, jopts sshtest.Options) *jumpSetup {
	t.Helper()
	env := testkit.NewEnv(t)
	jopts.Exec = echoExec
	jsrv := testkit.Start(t, jopts)
	tsrv := testkit.Start(t, sshtest.Options{Exec: echoExec, SFTP: true})
	jump := env.PasswordServer(t, jsrv, "web-1")
	target := env.PasswordServer(t, tsrv, "db-1")
	target.Jump = jump.ID
	env.Inv.Servers = append(env.Inv.Servers, jump, target)
	return &jumpSetup{env: env, jsrv: jsrv, tsrv: tsrv, jump: jump, target: target}
}

func run(t *testing.T, c sshx.Client, cmd string) string {
	t.Helper()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	out, err := sess.Output(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestDialThroughJump(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	c, err := j.env.Pool.Get(context.Background(), j.target)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, c, "echo hi"); got != "hi" {
		t.Fatalf("output %q", got)
	}
	if n := j.jsrv.Conns(); n != 1 {
		t.Fatalf("jump conns = %d", n)
	}
}

func TestDialJumpSFTP(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	c, err := j.env.Pool.Get(context.Background(), j.target)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := c.NewSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	f, err := sc.Create("/note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if fi, err := sc.Stat("/note.txt"); err != nil || fi.Size() != 2 {
		t.Fatalf("stat %v %v", fi, err)
	}
}

// prompts answers every prompt with reply and records what was asked.
type prompts struct {
	mu   sync.Mutex
	seen []sshx.PromptRequest
}

func (p *prompts) list() []sshx.PromptRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sshx.PromptRequest(nil), p.seen...)
}

func answer(t *testing.T, q *sshx.Queue, reply func(sshx.PromptRequest) sshx.PromptReply) *prompts {
	p := &prompts{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			select {
			case pend := <-q.C:
				p.mu.Lock()
				p.seen = append(p.seen, pend.Req)
				p.mu.Unlock()
				pend.Reply(reply(pend.Req))
			case <-ctx.Done():
				return
			}
		}
	}()
	return p
}

func TestPoolJumpAndTargetShareOneDial(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	j.env.Store.Delete(secrets.LoginAccount(j.jump.ID))
	j.jump.SavePassword = false
	j.env.Inv.Servers[0] = j.jump
	p := answer(t, j.env.Prompts, func(sshx.PromptRequest) sshx.PromptReply {
		return sshx.PromptReply{Text: testkit.Password, Accepted: true}
	})
	errs := make(chan error, 2)
	for _, s := range []inventory.Server{j.jump, j.target} {
		go func() {
			_, err := j.env.Pool.Get(context.Background(), s)
			errs <- err
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	seen := p.list()
	if len(seen) != 1 || seen[0].Kind != sshx.PromptPassword || seen[0].Server.ID != j.jump.ID {
		t.Fatalf("prompts %+v", seen)
	}
	if n := j.jsrv.Conns(); n != 1 {
		t.Fatalf("jump conns = %d", n)
	}
}

func TestDialJumpPromptsJumpThenTarget(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	if err := os.WriteFile(j.env.KnownHosts, nil, 0600); err != nil {
		t.Fatal(err)
	}
	p := answer(t, j.env.Prompts, func(sshx.PromptRequest) sshx.PromptReply {
		return sshx.PromptReply{Text: "yes", Accepted: true}
	})
	if _, err := j.env.Pool.Get(context.Background(), j.target); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range p.list() {
		if r.Kind == sshx.PromptHostKey {
			names = append(names, r.Server.Name)
		}
	}
	if strings.Join(names, ",") != "web-1,db-1" {
		t.Fatalf("host key prompts %v", names)
	}
	b, _ := os.ReadFile(j.env.KnownHosts)
	if !strings.Contains(string(b), "[127.0.0.1]:"+strconv.Itoa(j.tsrv.Port)+" ") {
		t.Fatalf("target not keyed by its address:\n%s", b)
	}
}

func TestDialJumpForwardingDenied(t *testing.T) {
	j := newJump(t, sshtest.Options{DenyForward: true})
	_, err := j.env.Pool.Get(context.Background(), j.target)
	var je *sshx.JumpError
	if !errors.Is(err, sshx.ErrForwardingDenied) || !errors.As(err, &je) || je.Jump.Name != "web-1" {
		t.Fatalf("err = %v", err)
	}
}

func closedPort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestDialJumpTargetRefused(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	j.target.Port = closedPort(t)
	_, err := j.env.Pool.Get(context.Background(), j.target)
	var ue *sshx.UnreachableError
	if !errors.As(err, &ue) || ue.Via != "web-1" || ue.Reason != "connection refused" {
		t.Fatalf("err = %#v", err)
	}
	if want := "via web-1 (connection refused)"; !strings.Contains(err.Error(), want) {
		t.Fatalf("err %q lacks %q", err, want)
	}
}

func TestDialJumpSilentTarget(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	j.target.Port = ln.Addr().(*net.TCPAddr).Port
	j.env.Dialer.Timeout = 500 * time.Millisecond
	done := make(chan error, 1)
	go func() { _, err := j.env.Pool.Get(context.Background(), j.target); done <- err }()
	select {
	case err := <-done:
		var ue *sshx.UnreachableError
		if !errors.As(err, &ue) || ue.Reason != "timeout" || ue.Via != "web-1" {
			t.Fatalf("err = %#v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dial hung")
	}
}

func TestDialJumpGuards(t *testing.T) {
	cases := []struct {
		name string
		mod  func(j *jumpSetup) inventory.Server
		want string
	}{
		{"self", func(j *jumpSetup) inventory.Server { j.target.Jump = j.target.ID; return j.target }, "a server can't jump through itself"},
		{"missing", func(j *jumpSetup) inventory.Server { j.target.Jump = "deadbeef"; return j.target }, "jump server not found"},
		{"two hops", func(j *jumpSetup) inventory.Server {
			app := j.jump
			app.ID, app.Name, app.Jump = "a1b2c3d4", "app-1", ""
			j.env.Inv.Servers[0].Jump = app.ID
			j.env.Inv.Servers = append(j.env.Inv.Servers, app)
			return j.target
		}, "web-1 itself jumps through app-1; only one hop is supported"},
		{"cycle", func(j *jumpSetup) inventory.Server {
			j.env.Inv.Servers[0].Jump = j.target.ID
			return j.target
		}, "web-1 itself jumps through db-1; only one hop is supported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := newJump(t, sshtest.Options{Forward: true})
			s := c.mod(j)
			done := make(chan error, 1)
			go func() { _, err := j.env.Pool.Get(context.Background(), s); done <- err }()
			select {
			case err := <-done:
				var je *sshx.JumpError
				if !errors.As(err, &je) || je.Err.Error() != c.want {
					t.Fatalf("err = %v, want JumpError %q", err, c.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("deadlock")
			}
		})
	}
}

func TestDialJumpNoHook(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	d := &sshx.Dialer{KnownHosts: j.env.KnownHosts, Creds: j.env.Creds}
	_, err := d.Dial(context.Background(), j.target)
	var je *sshx.JumpError
	if !errors.As(err, &je) || je.Err.Error() != "jump hosts are not set up" {
		t.Fatalf("err = %v", err)
	}
}

func TestCloseTargetKeepsJump(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	ctx := context.Background()
	jc, err := j.env.Pool.Get(ctx, j.jump)
	if err != nil {
		t.Fatal(err)
	}
	tc, err := j.env.Pool.Get(ctx, j.target)
	if err != nil {
		t.Fatal(err)
	}
	tc.Close()
	again, err := j.env.Pool.Get(ctx, j.jump)
	if err != nil || again != jc {
		t.Fatalf("jump replaced: %v", err)
	}
	if got := run(t, again, "echo still"); got != "still" {
		t.Fatalf("output %q", got)
	}
}

func TestEvictJumpDropsTunnel(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	ctx := context.Background()
	first, err := j.env.Pool.Get(ctx, j.target)
	if err != nil {
		t.Fatal(err)
	}
	j.env.Pool.Evict(j.jump.ID)
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := j.env.Pool.Get(ctx, j.target)
		if err != nil {
			t.Fatal(err)
		}
		if c != first {
			if got := run(t, c, "echo back"); got != "back" {
				t.Fatalf("output %q", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("tunnel through the evicted jump is still handed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestJumpAuthErrorIsJumps(t *testing.T) {
	j := newJump(t, sshtest.Options{Forward: true})
	j.env.Store.Set(secrets.LoginAccount(j.jump.ID), "wrong")
	_, err := j.env.Pool.Get(context.Background(), j.target)
	var ae *sshx.AuthError
	var je *sshx.JumpError
	if !errors.As(err, &je) || !errors.As(err, &ae) || ae.Server.ID != j.jump.ID {
		t.Fatalf("err = %v", err)
	}
}
