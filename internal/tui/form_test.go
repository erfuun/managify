package tui

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
)

// waitForWithout waits until all subs appear, failing if forbidden ever shows up.
func waitForWithout(t *testing.T, tm *teatest.TestModel, forbidden string, subs ...string) {
	t.Helper()
	seen := false
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		if bytes.Contains(b, []byte(forbidden)) {
			seen = true
			return true
		}
		for _, s := range subs {
			if !bytes.Contains(b, []byte(s)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
	if seen {
		t.Fatalf("output showed %q", forbidden)
	}
}

func mem(d Deps) *secrets.Mem { return d.Secrets.(*secrets.Mem) }

// fillDB types db-1's fields and switches auth to password; focus ends on Password.
func fillDB(tm *teatest.TestModel) {
	press(tm, "a")
	typeText(tm, "db-1")
	press(tm, "tab")
	typeText(tm, "203.0.113.20")
	press(tm, "tab", "tab") // port keeps its default 22
	typeText(tm, "admin")
	press(tm, "tab", "space", "tab") // auth → password, then Password field
	typeText(tm, "hunter2")
}

func TestAddPasswordServerSavesToKeychain(t *testing.T) {
	d := testDeps(t)
	tm := start(t, d)
	fillDB(tm)
	waitForWithout(t, tm, "hunter2", "Add server", "*******", "[x] Save password to keychain")
	press(tm, "tab", "enter")
	waitFor(t, tm, "admin@203.0.113.20")
	press(tm, "q")
	final(t, tm)

	if len(d.Inv.Servers) != 1 {
		t.Fatalf("servers = %+v", d.Inv.Servers)
	}
	s := d.Inv.Servers[0]
	if s.Auth != inventory.AuthPassword || !s.SavePassword || s.Port != 22 {
		t.Fatalf("server = %+v", s)
	}
	if pw, err := mem(d).Get(secrets.LoginAccount(s.ID)); err != nil || pw != "hunter2" {
		t.Fatalf("keychain = %q, %v", pw, err)
	}
	reloaded := reload(t, d)
	if len(reloaded.Servers) != 1 {
		t.Fatal("servers.yaml was not saved")
	}
	if bytes.Contains(readInv(t, d), []byte("hunter2")) {
		t.Fatal("servers.yaml contains the password")
	}
}

func TestAddPasswordNotSavedGoesToSession(t *testing.T) {
	d := testDeps(t)
	tm := start(t, d)
	fillDB(tm)
	press(tm, "tab", "space") // uncheck save
	waitFor(t, tm, "[ ] Save password to keychain")
	press(tm, "enter")
	waitFor(t, tm, "admin@203.0.113.20")
	press(tm, "q")
	final(t, tm)

	id := d.Inv.Servers[0].ID
	if _, err := mem(d).Get(secrets.LoginAccount(id)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("keychain should be empty: %v", err)
	}
	if pw, ok := d.Session.Get(secrets.LoginAccount(id)); !ok || pw != "hunter2" {
		t.Fatalf("session = %q %v", pw, ok)
	}
}

func TestValidationErrorKeepsForm(t *testing.T) {
	d := testDeps(t)
	tm := start(t, d)
	press(tm, "a")
	// Key auth shows 7 fields; enter on the last one submits.
	press(tm, "enter", "enter", "enter", "enter", "enter", "enter", "enter")
	waitFor(t, tm, "name is required", "Add server")
	press(tm, "esc")
	waitFor(t, tm, "No servers yet")
	press(tm, "q")
	final(t, tm)
	if len(d.Inv.Servers) != 0 {
		t.Fatal("invalid server was added")
	}
}

func TestDeleteRemovesSecrets(t *testing.T) {
	d := testDeps(t, dbServer)
	id := d.Inv.Servers[0].ID
	mem(d).Set(secrets.LoginAccount(id), "old")
	mem(d).Set(secrets.SudoAccount(id), "sudo-old")
	d.Session.Set(secrets.LoginAccount(id), "old")
	tm := start(t, d)
	waitFor(t, tm, "db-1")
	press(tm, "d")
	waitFor(t, tm, "Delete db-1? (y/n)")
	press(tm, "y")
	waitFor(t, tm, "No servers yet")
	press(tm, "q")
	final(t, tm)

	if len(d.Inv.Servers) != 0 || len(reload(t, d).Servers) != 0 {
		t.Fatal("server not deleted and saved")
	}
	for _, acct := range []string{secrets.LoginAccount(id), secrets.SudoAccount(id)} {
		if _, err := mem(d).Get(acct); !errors.Is(err, secrets.ErrNotFound) {
			t.Fatalf("%s still in keychain", acct)
		}
	}
	if _, ok := d.Session.Get(secrets.LoginAccount(id)); ok {
		t.Fatal("session still has the password")
	}
}

func TestDeleteCancelled(t *testing.T) {
	d := testDeps(t, dbServer)
	tm := start(t, d)
	waitFor(t, tm, "db-1")
	press(tm, "d")
	waitFor(t, tm, "Delete db-1? (y/n)")
	press(tm, "n", "q")
	final(t, tm)
	if len(d.Inv.Servers) != 1 {
		t.Fatal("deleted after n")
	}
}

func TestEditKeepsPasswordWhenBlank(t *testing.T) {
	d := testDeps(t, dbServer)
	id := d.Inv.Servers[0].ID
	mem(d).Set(secrets.LoginAccount(id), "old")
	tm := start(t, d)
	waitFor(t, tm, "db-1")
	press(tm, "e")
	waitFor(t, tm, "Edit db-1")
	typeText(tm, "x") // name → db-1x
	// Name, Host, Port, User, Auth, Password, Save: tab to Save, enter.
	press(tm, "tab", "tab", "tab", "tab", "tab", "tab", "enter")
	waitFor(t, tm, "db-1x")
	press(tm, "q")
	final(t, tm)

	s, _ := d.Inv.Get(id)
	if s.Name != "db-1x" {
		t.Fatalf("name = %q", s.Name)
	}
	if pw, _ := mem(d).Get(secrets.LoginAccount(id)); pw != "old" {
		t.Fatalf("password changed to %q", pw)
	}
}

func TestSwitchAuthRemovesOldSecret(t *testing.T) {
	d := testDeps(t, dbServer)
	id := d.Inv.Servers[0].ID
	mem(d).Set(secrets.LoginAccount(id), "old")
	d.Session.Set(secrets.LoginAccount(id), "old")
	tm := start(t, d)
	waitFor(t, tm, "db-1")
	press(tm, "e")
	waitFor(t, tm, "Edit db-1")
	press(tm, "tab", "tab", "tab", "tab", "space") // Auth → key
	waitFor(t, tm, "Key path")
	press(tm, "enter", "enter", "enter") // Auth → Key path → Sudo → submit
	waitFor(t, tm, "admin@203.0.113.20")
	press(tm, "q")
	final(t, tm)

	s, _ := d.Inv.Get(id)
	if s.Auth != inventory.AuthKey {
		t.Fatalf("auth = %q", s.Auth)
	}
	if _, err := mem(d).Get(secrets.LoginAccount(id)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("old password left in keychain")
	}
	if _, ok := d.Session.Get(secrets.LoginAccount(id)); ok {
		t.Fatal("old password left in session")
	}
}

func TestKeyPathAcceptsSpaces(t *testing.T) {
	d := testDeps(t)
	tm := start(t, d)
	press(tm, "a")
	typeText(tm, "web-1")
	press(tm, "tab")
	typeText(tm, "203.0.113.10")
	press(tm, "tab", "tab")
	typeText(tm, "root")
	press(tm, "tab", "tab") // stay on key auth, go to Key path
	typeText(tm, `C:\My`)
	press(tm, "space")
	typeText(tm, `Keys\id`)
	press(tm, "enter", "enter") // → Sudo → submit
	waitFor(t, tm, "root@203.0.113.10")
	press(tm, "q")
	final(t, tm)
	if got := d.Inv.Servers[0].KeyPath; got != `C:\My Keys\id` {
		t.Fatalf("key path = %q", got)
	}
}

func reload(t *testing.T, d Deps) *inventory.Inventory {
	t.Helper()
	inv, err := inventory.Load(invPath(t, d))
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func readInv(t *testing.T, d Deps) []byte {
	t.Helper()
	b, err := os.ReadFile(invPath(t, d))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
