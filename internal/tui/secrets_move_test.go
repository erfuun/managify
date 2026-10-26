package tui

import (
	"errors"
	"testing"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
)

// Changing where a secret lives without retyping it must move it, not drop it.
func TestBlankPasswordMovesSecret(t *testing.T) {
	type where int
	const (
		keychain where = iota
		session
	)
	cases := []struct {
		name     string
		server   inventory.Server
		account  func(string) string
		from, to where
	}{
		{"login saved→not saved", inventory.Server{Auth: inventory.AuthPassword, SavePassword: false}, secrets.LoginAccount, keychain, session},
		{"login not saved→saved", inventory.Server{Auth: inventory.AuthPassword, SavePassword: true}, secrets.LoginAccount, session, keychain},
		{"sudo saved→ask", inventory.Server{Auth: inventory.AuthKey, SudoPassword: inventory.SudoAsk}, secrets.SudoAccount, keychain, session},
		{"sudo ask→saved", inventory.Server{Auth: inventory.AuthKey, SudoPassword: inventory.SudoSaved}, secrets.SudoAccount, session, keychain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := testDeps(t)
			m := New(d)
			s := c.server
			s.ID = "7f3a9c21"
			acct := c.account(s.ID)
			if c.from == keychain {
				mem(d).Set(acct, "old")
			} else {
				d.Session.Set(acct, "old")
			}
			if err := m.storeSecrets(s, "", ""); err != nil {
				t.Fatal(err)
			}
			kv, kerr := mem(d).Get(acct)
			sv, sok := d.Session.Get(acct)
			if c.to == keychain && (kerr != nil || kv != "old") {
				t.Fatalf("keychain = %q, %v", kv, kerr)
			}
			if c.to == session {
				if !sok || sv != "old" {
					t.Fatalf("session = %q, %v", sv, sok)
				}
				if !errors.Is(kerr, secrets.ErrNotFound) {
					t.Fatal("secret left in keychain")
				}
			}
		})
	}
}

func TestBlankPortDefaultsTo22(t *testing.T) {
	d := testDeps(t)
	tm := start(t, d)
	press(tm, "a")
	typeText(tm, "web-1")
	press(tm, "tab")
	typeText(tm, "203.0.113.10")
	press(tm, "tab")
	tm.Send(keyBackspaces(2))
	press(tm, "tab")
	typeText(tm, "root")
	press(tm, "tab", "enter", "enter", "enter") // Auth → Key path → Sudo → submit
	waitFor(t, tm, "root@203.0.113.10")
	press(tm, "q")
	final(t, tm)
	if d.Inv.Servers[0].Port != 22 {
		t.Fatalf("port = %d", d.Inv.Servers[0].Port)
	}
}
