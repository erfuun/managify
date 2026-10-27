package sshx

import (
	"context"
	"sync"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
)

// Creds finds passwords and passphrases: keychain, then this session's
// memory, then a prompt. Keychain failures fall through rather than abort.
type Creds struct {
	Store    secrets.Store
	Session  *secrets.Session
	Prompter Prompter

	mu    sync.Mutex
	locks map[string]chan struct{} // per account, so one secret is asked for once
}

func (c *Creds) LoginPassword(ctx context.Context, s inventory.Server) (string, error) {
	return c.lookup(ctx, s, secrets.LoginAccount(s.ID), s.SavePassword, PromptRequest{Server: s, Kind: PromptPassword})
}

func (c *Creds) Passphrase(ctx context.Context, s inventory.Server, keyPath string) (string, error) {
	return c.lookup(ctx, s, passphraseKey(keyPath), false, PromptRequest{Server: s, Kind: PromptPassphrase, Detail: keyPath})
}

// SudoPassword returns "" for servers with passwordless (NOPASSWD) sudo.
func (c *Creds) SudoPassword(ctx context.Context, s inventory.Server) (string, error) {
	if s.Auth == inventory.AuthPassword {
		return c.LoginPassword(ctx, s)
	}
	acct := secrets.SudoAccount(s.ID)
	switch s.SudoPassword {
	case inventory.SudoSaved:
		return c.lookup(ctx, s, acct, true, PromptRequest{Server: s, Kind: PromptSudo})
	case inventory.SudoAsk:
		return c.lookup(ctx, s, acct, false, PromptRequest{Server: s, Kind: PromptSudo})
	default:
		return "", nil
	}
}

// ForgetLogin drops the session copy after a rejected login. A saved keychain
// entry stays until the user edits the server.
func (c *Creds) ForgetLogin(s inventory.Server) { c.Session.Forget(secrets.LoginAccount(s.ID)) }

func (c *Creds) ForgetPassphrase(keyPath string) { c.Session.Forget(passphraseKey(keyPath)) }

func (c *Creds) ForgetSudo(s inventory.Server) { c.Session.Forget(secrets.SudoAccount(s.ID)) }

func (c *Creds) lookup(ctx context.Context, s inventory.Server, acct string, keychain bool, req PromptRequest) (string, error) {
	lock := c.lockFor(acct)
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return "", ErrPromptCancelled
	}
	defer func() { <-lock }()
	// Checked under the lock: a concurrent caller may have just been answered.
	if keychain {
		if v, err := c.Store.Get(acct); err == nil && v != "" {
			return v, nil
		}
	}
	if v, ok := c.Session.Get(acct); ok {
		return v, nil
	}
	rep, err := c.Prompter.Ask(ctx, req)
	if err != nil {
		return "", err
	}
	c.Session.Set(acct, rep.Text)
	if keychain {
		_ = c.Store.Set(acct, rep.Text) // on failure the session copy still works
	}
	return rep.Text, nil
}

func (c *Creds) lockFor(acct string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks == nil {
		c.locks = map[string]chan struct{}{}
	}
	l, ok := c.locks[acct]
	if !ok {
		l = make(chan struct{}, 1)
		c.locks[acct] = l
	}
	return l
}

func passphraseKey(keyPath string) string { return "pass:" + keyPath }
