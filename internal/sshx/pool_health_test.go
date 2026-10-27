package sshx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/erfuun/managify/internal/inventory"
)

type fakeClient struct {
	keepalive  func() error
	keepalives atomic.Int32
	closes     atomic.Int32
}

func (c *fakeClient) NewSession() (*ssh.Session, error) { return nil, errors.New("fake") }
func (c *fakeClient) NewSFTP() (*sftp.Client, error)    { return nil, errors.New("fake") }
func (c *fakeClient) Close() error                      { c.closes.Add(1); return nil }
func (c *fakeClient) SendKeepalive() error {
	c.keepalives.Add(1)
	if c.keepalive != nil {
		return c.keepalive()
	}
	return nil
}

// fakeDialer hands out the given clients in order.
type fakeDialer struct {
	mu      sync.Mutex
	clients []*fakeClient
	dials   int
}

func (d *fakeDialer) Dial(context.Context, inventory.Server) (Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.clients[d.dials]
	d.dials++
	return c, nil
}

func healthPool(clients ...*fakeClient) (*Pool, *fakeDialer) {
	d := &fakeDialer{clients: clients}
	p := NewPool(d)
	p.Keepalive = time.Hour // the background loop stays out of these tests
	p.CheckAfter = time.Millisecond
	p.CheckTimeout = 100 * time.Millisecond
	return p, d
}

var healthServer = inventory.Server{ID: "a", Host: "h", Port: 22, User: "u", Auth: inventory.AuthKey}

func TestPoolHalfOpenConnectionIsReplaced(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	stuck := &fakeClient{keepalive: func() error { <-block; return nil }}
	fresh := &fakeClient{}
	p, d := healthPool(stuck, fresh)
	if c := mustGet(t, p, healthServer); c != stuck {
		t.Fatal("first Get did not dial")
	}
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	if c := mustGet(t, p, healthServer); c != fresh {
		t.Fatal("a connection that did not answer was reused")
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("Get took %v", el)
	}
	// A slow answer may be a busy link (an upload filling the send buffer):
	// stop handing it out, but leave it to finish what it is doing.
	if n := stuck.closes.Load(); n != 0 {
		t.Fatalf("slow connection closed %d times", n)
	}
	if d.dials != 2 {
		t.Fatalf("dials = %d", d.dials)
	}
	p.Close()
	if stuck.closes.Load() == 0 || fresh.closes.Load() == 0 {
		t.Fatalf("Close left connections open: stuck %d fresh %d", stuck.closes.Load(), fresh.closes.Load())
	}
}

func TestPoolDeadConnectionIsClosedAndReplaced(t *testing.T) {
	dead := &fakeClient{keepalive: func() error { return errors.New("broken pipe") }}
	fresh := &fakeClient{}
	p, _ := healthPool(dead, fresh)
	defer p.Close()
	mustGet(t, p, healthServer)
	time.Sleep(20 * time.Millisecond)
	if c := mustGet(t, p, healthServer); c != fresh {
		t.Fatal("dead connection reused")
	}
	if dead.closes.Load() == 0 {
		t.Fatal("dead connection not closed")
	}
}

func TestPoolRecentConnectionNotChecked(t *testing.T) {
	c := &fakeClient{}
	p, _ := healthPool(c)
	defer p.Close()
	p.CheckAfter = time.Hour
	mustGet(t, p, healthServer)
	mustGet(t, p, healthServer)
	if n := c.keepalives.Load(); n != 0 {
		t.Fatalf("checked %d times", n)
	}
}

func TestPoolIdleHealthyConnectionReused(t *testing.T) {
	c := &fakeClient{}
	p, d := healthPool(c)
	defer p.Close()
	mustGet(t, p, healthServer)
	time.Sleep(20 * time.Millisecond)
	if got := mustGet(t, p, healthServer); got != c || d.dials != 1 || c.keepalives.Load() != 1 {
		t.Fatalf("same=%v dials=%d keepalives=%d", got == c, d.dials, c.keepalives.Load())
	}
}
