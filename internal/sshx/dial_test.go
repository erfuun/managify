package sshx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/sshtest"
)

func noAgent() (agent.Agent, error) { return nil, errors.New("no agent") }

// seededDialer trusts srv's ed25519 host key and has no agent.
func seededDialer(t *testing.T, srv *sshtest.Server, c *Creds) *Dialer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(srv.KnownHostsLine()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return &Dialer{KnownHosts: path, Creds: c, Agent: noAgent}
}

func serverFor(srv *sshtest.Server, s inventory.Server) inventory.Server {
	s.Host, s.Port = srv.Host, srv.Port
	if s.ID == "" {
		s.ID = "7f3a9c21"
	}
	if s.User == "" {
		s.User = "root"
	}
	return s
}

func keyServer(srv *sshtest.Server, path string) inventory.Server {
	return serverFor(srv, inventory.Server{Name: "web-1", Auth: inventory.AuthKey, KeyPath: path})
}

func passServer(srv *sshtest.Server, save bool) inventory.Server {
	return serverFor(srv, inventory.Server{Name: "db-1", Auth: inventory.AuthPassword, SavePassword: save})
}

func mustDial(t *testing.T, d *Dialer, s inventory.Server) Client {
	t.Helper()
	c, err := d.Dial(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDialKeyFile(t *testing.T) {
	signer, path := sshtest.ClientKey(t)
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	c := mustDial(t, seededDialer(t, srv, newCreds(secrets.NewMem(), &fakePrompter{})), keyServer(srv, path))
	if err := c.SendKeepalive(); err != nil {
		t.Fatalf("keepalive on a healthy connection: %v", err)
	}
}

func TestDialEncryptedKeyPromptsPassphraseOnce(t *testing.T) {
	signer, path := sshtest.EncryptedClientKey(t, "phrase")
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	p := &fakePrompter{Answer: "phrase"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	mustDial(t, d, keyServer(srv, path))
	mustDial(t, d, keyServer(srv, path))
	if p.count(PromptPassphrase) != 1 {
		t.Fatalf("passphrase prompts = %d", p.count(PromptPassphrase))
	}
}

func TestDialWrongPassphrase(t *testing.T) {
	signer, path := sshtest.EncryptedClientKey(t, "phrase")
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	p := &fakePrompter{Answer: "wrong"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	for i := 0; i < 2; i++ {
		_, err := d.Dial(context.Background(), keyServer(srv, path))
		var ae *AuthError
		if !errors.As(err, &ae) || !ae.PassphraseWrong {
			t.Fatalf("err = %v", err)
		}
	}
	if p.count(PromptPassphrase) != 2 {
		t.Fatal("wrong passphrase was cached")
	}
}

func TestDialWrongKey(t *testing.T) {
	signer, _ := sshtest.ClientKey(t)
	_, otherPath := sshtest.ClientKey(t)
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	_, err := seededDialer(t, srv, newCreds(secrets.NewMem(), &fakePrompter{})).Dial(context.Background(), keyServer(srv, otherPath))
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Method != inventory.AuthKey || ae.PassphraseWrong {
		t.Fatalf("err = %v", err)
	}
}

func TestDialPasswordFromKeychain(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	store := secrets.NewMem()
	s := passServer(srv, true)
	store.Set(secrets.LoginAccount(s.ID), "hunter2")
	p := &fakePrompter{}
	mustDial(t, seededDialer(t, srv, newCreds(store, p)), s)
	if p.count(PromptPassword) != 0 {
		t.Fatal("prompted despite keychain")
	}
}

func TestDialPasswordPromptedAndCached(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	p := &fakePrompter{Answer: "hunter2"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	mustDial(t, d, passServer(srv, false))
	mustDial(t, d, passServer(srv, false))
	if p.count(PromptPassword) != 1 {
		t.Fatalf("password prompts = %d", p.count(PromptPassword))
	}
}

func TestDialWrongPasswordForgetsSession(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	c := newCreds(secrets.NewMem(), &fakePrompter{Answer: "wrong"})
	s := passServer(srv, false)
	_, err := seededDialer(t, srv, c).Dial(context.Background(), s)
	var ae *AuthError
	if !errors.As(err, &ae) || ae.Method != inventory.AuthPassword {
		t.Fatalf("err = %v", err)
	}
	if _, ok := c.Session.Get(secrets.LoginAccount(s.ID)); ok {
		t.Fatal("rejected password still cached")
	}
}

func TestDialPromptCancelledIsNotAuthError(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	_, err := seededDialer(t, srv, newCreds(secrets.NewMem(), &fakePrompter{Err: ErrPromptCancelled})).
		Dial(context.Background(), passServer(srv, false))
	var ae *AuthError
	if !errors.Is(err, ErrPromptCancelled) || errors.As(err, &ae) {
		t.Fatalf("err = %v", err)
	}
}

// After a cancel, keyboard-interactive must not ask the same question again.
func TestDialCancelledPasswordAsksOnce(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	p := &fakePrompter{Err: ErrPromptCancelled}
	_, err := seededDialer(t, srv, newCreds(secrets.NewMem(), p)).Dial(context.Background(), passServer(srv, false))
	if !errors.Is(err, ErrPromptCancelled) || p.count(PromptPassword) != 1 {
		t.Fatalf("err = %v, password prompts = %d", err, p.count(PromptPassword))
	}
}

func TestDialAgentUsedFirst(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: sshPub})
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), &fakePrompter{}))
	d.Agent = func() (agent.Agent, error) { return kr, nil }
	mustDial(t, d, keyServer(srv, ""))
}

func TestDialRefused(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	d := &Dialer{KnownHosts: filepath.Join(t.TempDir(), "kh"), Creds: newCreds(secrets.NewMem(), &fakePrompter{}), Agent: noAgent}
	_, err := d.Dial(context.Background(), inventory.Server{ID: "x", Name: "web-1", Host: "127.0.0.1", Port: port, User: "root", Auth: inventory.AuthKey})
	var ue *UnreachableError
	if !errors.As(err, &ue) || ue.Reason != "connection refused" || ue.Addr != "127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("err = %v", err)
	}
}

// silentListener accepts connections and never speaks.
func silentListener(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	})
	return ln.Addr().(*net.TCPAddr).Port
}

func TestDialTimeout(t *testing.T) {
	port := silentListener(t)
	d := &Dialer{KnownHosts: filepath.Join(t.TempDir(), "kh"), Creds: newCreds(secrets.NewMem(), &fakePrompter{}), Agent: noAgent, Timeout: 200 * time.Millisecond}
	start := time.Now()
	_, err := d.Dial(context.Background(), inventory.Server{ID: "x", Name: "web-1", Host: "127.0.0.1", Port: port, User: "root", Auth: inventory.AuthKey})
	var ue *UnreachableError
	if !errors.As(err, &ue) || ue.Reason != "timeout" {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestDialCtxCancelAbortsHandshake(t *testing.T) {
	port := silentListener(t)
	d := &Dialer{KnownHosts: filepath.Join(t.TempDir(), "kh"), Creds: newCreds(secrets.NewMem(), &fakePrompter{}), Agent: noAgent}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := d.Dial(ctx, inventory.Server{ID: "x", Name: "web-1", Host: "127.0.0.1", Port: port, User: "root", Auth: inventory.AuthKey})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	if !errors.Is(err, ErrPromptCancelled) {
		t.Fatalf("cancelled dial should read as cancelled, got %v", err)
	}
}

// slowPrompter answers after a delay, like a person typing.
type slowPrompter struct {
	delay  time.Duration
	answer string
	mu     sync.Mutex
	n      int
}

func (s *slowPrompter) Ask(context.Context, PromptRequest) (PromptReply, error) {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	time.Sleep(s.delay)
	return PromptReply{Text: s.answer, Accepted: true}, nil
}

func TestDialSlowPromptNotTimedOut(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), &slowPrompter{delay: 600 * time.Millisecond, answer: "hunter2"}))
	d.Timeout = 200 * time.Millisecond
	mustDial(t, d, passServer(srv, false))
}

func TestDialSlowHostKeyPromptNotTimedOut(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2"})
	store := secrets.NewMem()
	s := passServer(srv, true)
	store.Set(secrets.LoginAccount(s.ID), "hunter2")
	d := &Dialer{KnownHosts: filepath.Join(t.TempDir(), "kh"), Agent: noAgent, Timeout: 200 * time.Millisecond,
		Creds: newCreds(store, &slowPrompter{delay: 600 * time.Millisecond})}
	mustDial(t, d, s)
}

func TestDialSharedKeySlowPromptBothSucceed(t *testing.T) {
	signer, path := sshtest.EncryptedClientKey(t, "phrase")
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey()})
	p := &slowPrompter{delay: 600 * time.Millisecond, answer: "phrase"}
	d := seededDialer(t, srv, newCreds(secrets.NewMem(), p))
	d.Timeout = 200 * time.Millisecond
	errs := make(chan error, 2)
	for _, id := range []string{"a", "b"} {
		s := keyServer(srv, path)
		s.ID = id
		go func() {
			c, err := d.Dial(context.Background(), s)
			if err == nil {
				c.Close()
			}
			errs <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if p.n != 1 {
		t.Fatalf("prompts = %d", p.n)
	}
}

func TestDialKnownHostOtherKeyType(t *testing.T) {
	srv := sshtest.Start(t, sshtest.Options{Password: "hunter2", ECDSA: true})
	for name, key := range map[string]ssh.PublicKey{
		"ed25519 known": srv.HostSigner.PublicKey(),
		"ecdsa known":   srv.ECDSASigner.PublicKey(),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			os.WriteFile(path, []byte(srv.KnownHostsLineFor(key)+"\n"), 0600)
			store := secrets.NewMem()
			s := passServer(srv, true)
			store.Set(secrets.LoginAccount(s.ID), "hunter2")
			p := &fakePrompter{}
			d := &Dialer{KnownHosts: path, Creds: newCreds(store, p), Agent: noAgent}
			mustDial(t, d, s)
			if p.count(PromptHostKey) != 0 {
				t.Fatal("prompted for a known host")
			}
		})
	}
}
