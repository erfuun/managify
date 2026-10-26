package sshx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/sshtest"
)

func poolFor(t *testing.T, srv *sshtest.Server) (*Pool, inventory.Server) {
	t.Helper()
	store := secrets.NewMem()
	s := passServer(srv, true)
	store.Set(secrets.LoginAccount(s.ID), "hunter2")
	p := NewPool(seededDialer(t, srv, newCreds(store, &fakePrompter{})))
	t.Cleanup(p.Close)
	return p, s
}

func mustGet(t *testing.T, p *Pool, s inventory.Server) Client {
	t.Helper()
	c, err := p.Get(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPoolReuses(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	p, s := poolFor(t, srv)
	a, b := mustGet(t, p, s), mustGet(t, p, s)
	if a != b || srv.Conns() != 1 {
		t.Fatalf("same=%v conns=%d", a == b, srv.Conns())
	}
}

func TestPoolConcurrentGetDialsOnce(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	p, s := poolFor(t, srv)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); mustGet(t, p, s) }()
	}
	wg.Wait()
	if srv.Conns() != 1 {
		t.Fatalf("conns = %d", srv.Conns())
	}
}

func TestPoolRedialsAfterDrop(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	p, s := poolFor(t, srv)
	p.Keepalive = 50 * time.Millisecond
	mustGet(t, p, s)
	srv.DropAll()
	time.Sleep(200 * time.Millisecond)
	c := mustGet(t, p, s)
	if srv.Conns() != 2 {
		t.Fatalf("conns = %d", srv.Conns())
	}
	if _, err := c.NewSession(); err != nil {
		t.Fatalf("redialed client unusable: %v", err)
	}
}

func TestKeepaliveHealthyConnectionNotEvicted(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	p, s := poolFor(t, srv)
	p.Keepalive = 50 * time.Millisecond
	mustGet(t, p, s)
	time.Sleep(300 * time.Millisecond)
	mustGet(t, p, s)
	if srv.Conns() != 1 {
		t.Fatalf("healthy connection was evicted: conns = %d", srv.Conns())
	}
}

func TestPoolEvictsUnresponsiveConnection(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2", IgnoreGlobalRequests: true})
	p, s := poolFor(t, srv)
	p.Keepalive = 50 * time.Millisecond
	mustGet(t, p, s)
	time.Sleep(300 * time.Millisecond)
	mustGet(t, p, s)
	if srv.Conns() != 2 {
		t.Fatalf("unresponsive connection kept: conns = %d", srv.Conns())
	}
}

func TestPoolEditedServerDialsNew(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	p, s := poolFor(t, srv)
	first := mustGet(t, p, s)
	s.User = "admin"
	second := mustGet(t, p, s)
	if first == second || srv.Conns() != 2 {
		t.Fatalf("reused the old connection: conns = %d", srv.Conns())
	}
	if _, err := first.NewSession(); err == nil {
		t.Fatal("old connection left open")
	}
}

func TestPoolEvict(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	p, s := poolFor(t, srv)
	first := mustGet(t, p, s)
	p.Evict(s.ID)
	if _, err := first.NewSession(); err == nil {
		t.Fatal("evicted connection still open")
	}
	mustGet(t, p, s)
	if srv.Conns() != 2 {
		t.Fatalf("conns = %d", srv.Conns())
	}
}

func TestPoolDialErrorNotCached(t *testing.T) {
	calls := 0
	p := NewPool(dialFunc(func(context.Context, inventory.Server) (Client, error) {
		calls++
		return nil, errors.New("boom")
	}))
	defer p.Close()
	for i := 0; i < 2; i++ {
		if _, err := p.Get(context.Background(), inventory.Server{ID: "x"}); err == nil {
			t.Fatal("expected error")
		}
	}
	if calls != 2 {
		t.Fatalf("dials = %d", calls)
	}
}

func TestPoolGetCancelledWhileWaiting(t *testing.T) {
	release := make(chan struct{})
	p := NewPool(dialFunc(func(ctx context.Context, _ inventory.Server) (Client, error) {
		<-release
		return nil, errors.New("late")
	}))
	defer p.Close()
	go p.Get(context.Background(), inventory.Server{ID: "x"}) // holds the dial lock
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.Get(ctx, inventory.Server{ID: "x"})
	close(release)
	if !errors.Is(err, ErrPromptCancelled) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

type dialFunc func(context.Context, inventory.Server) (Client, error)

func (f dialFunc) Dial(ctx context.Context, s inventory.Server) (Client, error) { return f(ctx, s) }
