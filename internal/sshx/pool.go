package sshx

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/erfuun/managify/internal/inventory"
)

const (
	defaultKeepalive    = 30 * time.Second
	defaultCheckAfter   = 5 * time.Second
	defaultCheckTimeout = 5 * time.Second
)

var errPoolClosed = errors.New("connection pool closed")

type serverDialer interface {
	Dial(context.Context, inventory.Server) (Client, error)
}

// Pool keeps one live connection per server and replaces it when it dies or
// when the server's connection settings change.
type Pool struct {
	Keepalive time.Duration // 0 → 30s
	// A connection with no sign of life for CheckAfter is checked before
	// Get hands it out; one that does not answer within CheckTimeout is
	// replaced. Both 0 → 5s. This catches a link that died while the
	// laptop slept, long before the keepalive gives up on it.
	CheckAfter   time.Duration
	CheckTimeout time.Duration

	d       serverDialer
	mu      sync.Mutex
	entries map[string]*poolEntry
	retired map[*poolEntry]bool // replaced while slow, closed by their own keepalive or Close
	locks   map[string]chan struct{}
	closed  bool
}

type poolEntry struct {
	c      Client
	key    string    // inventory.Server.ConnKey at dial time
	lastOK time.Time // dial or last answered keepalive; guarded by Pool.mu
	stop   chan struct{}
	once   sync.Once
}

func (e *poolEntry) close() {
	e.once.Do(func() {
		close(e.stop)
		e.c.Close()
	})
}

func NewPool(d serverDialer) *Pool {
	return &Pool{d: d, entries: map[string]*poolEntry{}, retired: map[*poolEntry]bool{}, locks: map[string]chan struct{}{}}
}

// Get returns the server's live connection, dialing at most once even when
// many callers ask at the same moment.
func (p *Pool) Get(ctx context.Context, s inventory.Server) (Client, error) {
	lock := p.lockFor(s.ID)
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrPromptCancelled
	}
	defer func() { <-lock }()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errPoolClosed
	}
	if e := p.entries[s.ID]; e != nil {
		if e.key == s.ConnKey() {
			idle := time.Since(e.lastOK) >= orDefault(p.CheckAfter, defaultCheckAfter)
			p.mu.Unlock()
			if !idle || p.alive(s.ID, e) {
				return e.c, nil
			}
		} else {
			delete(p.entries, s.ID)
			p.mu.Unlock()
			defer e.close()
		}
	} else {
		p.mu.Unlock()
	}

	c, err := p.d.Dial(ctx, s)
	if err != nil {
		return nil, err
	}
	e := &poolEntry{c: c, key: s.ConnKey(), lastOK: time.Now(), stop: make(chan struct{})}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		c.Close()
		return nil, errPoolClosed
	}
	p.entries[s.ID] = e
	p.mu.Unlock()

	go p.keepalive(s.ID, e)
	if w, ok := c.(interface{ Wait() error }); ok {
		go func() {
			w.Wait()
			p.drop(s.ID, e)
		}()
	}
	return c, nil
}

func (p *Pool) Evict(serverID string) {
	p.mu.Lock()
	e := p.entries[serverID]
	delete(p.entries, serverID)
	p.mu.Unlock()
	if e != nil {
		e.close()
	}
}

func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	entries := p.entries
	retired := p.retired
	p.entries, p.retired = map[string]*poolEntry{}, map[*poolEntry]bool{}
	p.mu.Unlock()
	for _, e := range entries {
		e.close()
	}
	for e := range retired {
		e.close()
	}
}

// alive checks e with one keepalive. A failed one closes e. One that does
// not answer in time only retires e: the link may just be busy, say with an
// upload filling the send buffer, so whatever uses it is left to finish and
// e's own keepalive closes it if it really is dead.
func (p *Pool) alive(id string, e *poolEntry) bool {
	done := make(chan error, 1)
	go func() { done <- e.c.SendKeepalive() }()
	select {
	case err := <-done:
		if err != nil {
			p.drop(id, e)
			return false
		}
		p.markOK(e)
		return true
	case <-time.After(orDefault(p.CheckTimeout, defaultCheckTimeout)):
		p.mu.Lock()
		if p.entries[id] == e {
			delete(p.entries, id)
			p.retired[e] = true
		}
		p.mu.Unlock()
		return false
	}
}

func (p *Pool) markOK(e *poolEntry) {
	p.mu.Lock()
	e.lastOK = time.Now()
	p.mu.Unlock()
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// drop removes e only if it is still the server's current entry.
func (p *Pool) drop(id string, e *poolEntry) {
	p.mu.Lock()
	if p.entries[id] == e {
		delete(p.entries, id)
	}
	delete(p.retired, e)
	p.mu.Unlock()
	e.close()
}

// keepalive gives each probe one interval to answer. A half-open connection
// never replies, and closing it is what unblocks the stuck request.
func (p *Pool) keepalive(id string, e *poolEntry) {
	interval := p.Keepalive
	if interval <= 0 {
		interval = defaultKeepalive
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-t.C:
		}
		done := make(chan error, 1)
		go func() { done <- e.c.SendKeepalive() }()
		select {
		case err := <-done:
			if err != nil {
				p.drop(id, e)
				return
			}
			p.markOK(e)
		case <-time.After(interval):
			p.drop(id, e)
			return
		case <-e.stop:
			return
		}
	}
}

func (p *Pool) lockFor(id string) chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.locks[id]
	if !ok {
		l = make(chan struct{}, 1)
		p.locks[id] = l
	}
	return l
}
