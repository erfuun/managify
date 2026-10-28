// Package testkit wires real sshx connections to sshtest servers for tests
// outside sshx. Import it only from _test.go files.
package testkit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh/agent"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/sshx"
)

// Password is what Start configures and PasswordServer saves.
const Password = "hunter2"

// Start runs an sshtest server that accepts Password.
func Start(t testing.TB, o sshtest.Options) *sshtest.Server {
	o.Password = Password
	return sshtest.Start(t, o)
}

// Env is the connection stack Managify builds in main, with test stores.
type Env struct {
	KnownHosts string
	Store      *secrets.Mem
	Session    *secrets.Session
	Prompts    *sshx.Queue
	Creds      *sshx.Creds
	Dialer     *sshx.Dialer
	Pool       *sshx.Pool
	Inv        *inventory.Inventory // jump servers are looked up here
}

func NewEnv(t testing.TB) *Env {
	t.Helper()
	e := &Env{
		KnownHosts: filepath.Join(t.TempDir(), "known_hosts"),
		Store:      secrets.NewMem(),
		Session:    secrets.NewSession(),
		Prompts:    sshx.NewQueue(),
	}
	e.Creds = &sshx.Creds{Store: e.Store, Session: e.Session, Prompter: e.Prompts}
	e.Dialer = &sshx.Dialer{
		KnownHosts: e.KnownHosts,
		Creds:      e.Creds,
		Agent:      func() (agent.Agent, error) { return nil, errors.New("no agent") },
	}
	e.Pool = sshx.NewPool(e.Dialer)
	inv, err := inventory.Load(filepath.Join(t.TempDir(), "servers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	e.Inv = inv
	e.Dialer.Lookup, e.Dialer.Jump = inv.Get, e.Pool.Get
	t.Cleanup(e.Pool.Close)
	return e
}

// Trust records srv's host key in known_hosts.
func (e *Env) Trust(t testing.TB, srv *sshtest.Server) {
	t.Helper()
	f, err := os.OpenFile(e.KnownHosts, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(srv.KnownHostsLine() + "\n"); err != nil {
		t.Fatal(err)
	}
}

// PasswordServer is a trusted profile for srv with Password in the keychain.
func (e *Env) PasswordServer(t testing.TB, srv *sshtest.Server, name string) inventory.Server {
	t.Helper()
	e.Trust(t, srv)
	s := inventory.Server{
		ID: inventory.NewID(), Name: name, Host: srv.Host, Port: srv.Port,
		User: "root", Auth: inventory.AuthPassword, SavePassword: true,
	}
	e.Store.Set(secrets.LoginAccount(s.ID), Password)
	return s
}

// Dial connects to srv (started with Start) and closes the client in cleanup.
func Dial(t testing.TB, srv *sshtest.Server) sshx.Client {
	t.Helper()
	e := NewEnv(t)
	c, err := e.Dialer.Dial(context.Background(), e.PasswordServer(t, srv, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
