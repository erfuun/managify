package sshx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/erfuun/managify/internal/inventory"
)

// Client is one live SSH connection.
type Client interface {
	NewSession() (*ssh.Session, error)
	// DialContext opens a TCP connection from the server's side, which is
	// how a jump server reaches the servers behind it.
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	NewSFTP() (*sftp.Client, error)
	SendKeepalive() error
	Close() error
}

type conn struct{ *ssh.Client }

func (c conn) NewSFTP() (*sftp.Client, error) { return sftp.NewClient(c.Client) }

// SendKeepalive treats any reply as alive: OpenSSH answers this request with
// "unsupported", which still proves the connection works.
func (c conn) SendKeepalive() error {
	_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
	return err
}

type Dialer struct {
	KnownHosts string
	Creds      *Creds
	Timeout    time.Duration               // 0 → 10s
	Agent      func() (agent.Agent, error) // nil → DefaultAgent
	// Lookup finds a saved server by ID. Jump returns the live connection to
	// a jump server. Either nil means jumps are unsupported.
	Lookup func(id string) (inventory.Server, bool)
	Jump   func(ctx context.Context, jump inventory.Server) (Client, error)
}

const defaultTimeout = 10 * time.Second

func (d *Dialer) Dial(ctx context.Context, s inventory.Server) (Client, error) {
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if s.Jump != "" {
		return d.dialVia(ctx, s, timeout)
	}
	addr := s.Addr()
	nd := net.Dialer{Timeout: timeout}
	raw, err := nd.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, netError(ctx, addr, err)
	}
	return d.handshake(ctx, s, raw, timeout, "")
}

// dialVia reaches s through its jump server's pooled connection. The jump is
// checked before the pool is touched: the pool locks per server, so a
// self-jump or a cycle from a hand-edited servers.yaml would deadlock there.
func (d *Dialer) dialVia(ctx context.Context, s inventory.Server, timeout time.Duration) (Client, error) {
	unknown := inventory.Server{ID: s.Jump, Name: s.Jump}
	switch {
	case d.Lookup == nil || d.Jump == nil:
		return nil, &JumpError{Jump: unknown, Err: errors.New("jump hosts are not set up")}
	case s.Jump == s.ID:
		return nil, &JumpError{Jump: s, Err: errors.New("a server can't jump through itself")}
	}
	jump, ok := d.Lookup(s.Jump)
	if !ok {
		return nil, &JumpError{Jump: unknown, Err: errors.New("jump server not found")}
	}
	if jump.Jump != "" {
		next := jump.Jump
		if n, ok := d.Lookup(jump.Jump); ok {
			next = n.Name
		}
		return nil, &JumpError{Jump: jump, Err: fmt.Errorf("%s itself jumps through %s; only one hop is supported", jump.Name, next)}
	}
	jc, err := d.Jump(ctx, jump)
	if errors.Is(err, ErrPromptCancelled) {
		return nil, err
	}
	if err != nil {
		return nil, &JumpError{Jump: jump, Err: err}
	}
	octx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := jc.DialContext(octx, "tcp", s.Addr())
	if err != nil {
		return nil, openError(ctx, s, jump, err)
	}
	return d.handshake(ctx, s, raw, timeout, jump.Name)
}

// openError classifies a direct-tcpip channel the jump server did not open.
func openError(ctx context.Context, s, jump inventory.Server, err error) error {
	var oe *ssh.OpenChannelError
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return ErrPromptCancelled
	case errors.As(err, &oe) && oe.Reason == ssh.Prohibited:
		return &JumpError{Jump: jump, Err: ErrForwardingDenied}
	case errors.As(err, &oe):
		reason := oe.Message
		switch msg := strings.ToLower(oe.Message); {
		case strings.Contains(msg, "refused"):
			reason = "connection refused"
		case strings.Contains(msg, "timed out"), strings.Contains(msg, "timeout"):
			reason = "timeout"
		}
		return &UnreachableError{Addr: s.Addr(), Via: jump.Name, Reason: reason}
	}
	return withVia(netError(ctx, s.Addr(), err), jump.Name)
}

// handshake runs SSH over raw: a TCP connection, or a tunnel through via.
func (d *Dialer) handshake(ctx context.Context, s inventory.Server, raw net.Conn, timeout time.Duration, via string) (Client, error) {
	addr := s.Addr()
	pc := &pausableConn{Conn: raw, timeout: timeout}
	pc.resume()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()

	st := &dialState{}
	defer func() {
		for _, a := range st.agents {
			a.Close()
		}
	}()
	hostKeys, err := HostKeyCallback(ctx, d.KnownHosts, s, pausingPrompter{d.Creds.Prompter, pc})
	if err != nil {
		raw.Close()
		return nil, err
	}
	algos, err := KnownHostAlgorithms(d.KnownHosts, addr)
	if err != nil {
		raw.Close()
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:              s.User,
		Auth:              d.authMethods(ctx, s, pc, st),
		HostKeyCallback:   hostKeys,
		HostKeyAlgorithms: algos,
		Timeout:           timeout,
	}
	sc, chans, reqs, err := ssh.NewClientConn(pc, addr, cfg)
	if err != nil {
		raw.Close()
		if pc.expired() && ctx.Err() == nil {
			return nil, &UnreachableError{Addr: addr, Via: via, Reason: "timeout"}
		}
		return nil, withVia(d.handshakeError(ctx, s, st, err), via)
	}
	pc.disarm()
	return conn{ssh.NewClient(sc, chans, reqs)}, nil
}

func (d *Dialer) handshakeError(ctx context.Context, s inventory.Server, st *dialState, err error) error {
	var mismatch *HostKeyMismatchError
	switch {
	case errors.Is(st.err, ErrPromptCancelled), errors.Is(err, ErrPromptCancelled):
		return ErrPromptCancelled
	case st.err != nil:
		return st.err
	case errors.As(err, &mismatch):
		return mismatch
	case errors.Is(err, ErrHostKeyRejected):
		return ErrHostKeyRejected
	case strings.Contains(err.Error(), "unable to authenticate"):
		if s.Auth == inventory.AuthPassword {
			d.Creds.ForgetLogin(s)
		}
		return &AuthError{Server: s, Method: s.Auth}
	}
	return netError(ctx, s.Addr(), err)
}

// pausableConn holds the handshake deadline, lifted while a prompt is open so
// a person reading a fingerprint or typing a password is never timed out.
// A connection that refuses deadlines, such as a tunnel through a jump
// server, gets a timer that closes it instead.
type pausableConn struct {
	net.Conn
	timeout time.Duration
	mu      sync.Mutex
	paused  int
	probed  bool // SetDeadline tried once
	timed   bool // deadlines unsupported: timer is used
	timer   *time.Timer
	fired   bool // the timer closed the connection
	off     bool // handshake over
}

func (c *pausableConn) expired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fired
}

func (c *pausableConn) pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused++
	c.stop()
}

func (c *pausableConn) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused > 0 {
		c.paused--
	}
	if c.paused > 0 || c.off {
		return
	}
	if !c.probed {
		c.probed = true
		c.timed = c.Conn.SetDeadline(time.Now().Add(c.timeout)) != nil
		if !c.timed {
			return
		}
	}
	if !c.timed {
		c.Conn.SetDeadline(time.Now().Add(c.timeout))
		return
	}
	if c.timer == nil {
		c.timer = time.AfterFunc(c.timeout, func() {
			c.mu.Lock()
			c.fired = true
			c.mu.Unlock()
			c.Conn.Close()
		})
	} else {
		c.timer.Reset(c.timeout)
	}
}

// disarm ends all timing once the handshake is done.
func (c *pausableConn) disarm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.off = true
	c.stop()
}

func (c *pausableConn) stop() {
	if c.timed {
		if c.timer != nil {
			c.timer.Stop()
		}
		return
	}
	c.Conn.SetDeadline(time.Time{})
}

type pausingPrompter struct {
	inner Prompter
	pc    *pausableConn
}

func (p pausingPrompter) Ask(ctx context.Context, r PromptRequest) (PromptReply, error) {
	p.pc.pause()
	defer p.pc.resume()
	return p.inner.Ask(ctx, r)
}
