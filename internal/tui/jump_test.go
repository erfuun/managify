package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/sshx"
)

// jumpDeps holds web-1 and db-1, which jumps through web-1.
func jumpDeps(t *testing.T) (Deps, inventory.Server, inventory.Server) {
	t.Helper()
	d := testDeps(t, webServer)
	web := d.Inv.Servers[0]
	db := dbServer
	db.Jump = web.ID
	db, err := d.Inv.Add(db)
	if err != nil {
		t.Fatal(err)
	}
	return d, web, db
}

func formFor(t *testing.T, d Deps, s *inventory.Server) formModel {
	t.Helper()
	m, _ := New(d).openForm(s)
	return m.(Model).form
}

func TestFormJumpChoices(t *testing.T) {
	d, web, db := jumpDeps(t)
	if _, err := d.Inv.Add(inventory.Server{Name: "app-1", Host: "10.0.0.20", User: "root", Auth: inventory.AuthKey, Jump: web.ID}); err != nil {
		t.Fatal(err)
	}
	f := formFor(t, d, &db)
	if !slices.Equal(f.jumpNames, []string{"none", "web-1"}) {
		t.Fatalf("db-1 choices %v", f.jumpNames)
	}
	if f.jumpIDs[f.jump] != web.ID {
		t.Fatalf("db-1 starts on %q", f.jumpIDs[f.jump])
	}
	if f := formFor(t, d, &web); slices.Contains(f.visible(), fJump) {
		t.Fatal("a server others jump through offers a jump")
	}
}

func TestFormJumpShownForBothAuths(t *testing.T) {
	d, _, _ := jumpDeps(t)
	f := formFor(t, d, nil)
	for _, auth := range []inventory.Auth{inventory.AuthKey, inventory.AuthPassword} {
		f.auth = auth
		if !slices.Contains(f.visible(), fJump) {
			t.Fatalf("no Jump via field for %s auth", auth)
		}
	}
	if f := formFor(t, testDeps(t), nil); slices.Contains(f.visible(), fJump) {
		t.Fatal("Jump via shown with no server to jump through")
	}
}

func TestAddServerWithJump(t *testing.T) {
	d := testDeps(t, webServer)
	web := d.Inv.Servers[0]
	tm := start(t, d)
	waitFor(t, tm, "web-1")
	press(tm, "a")
	typeText(tm, "db-1")
	press(tm, "tab")
	typeText(tm, "10.0.0.12")
	press(tm, "tab", "tab")
	typeText(tm, "postgres")
	press(tm, "tab")
	waitFor(t, tm, "Jump via", "[none]")
	press(tm, "space")
	waitFor(t, tm, "[web-1]")
	press(tm, "tab", "tab", "tab", "enter") // auth key, key path, sudo; enter saves
	waitFor(t, tm, "postgres@10.0.0.12 via web-1")
	press(tm, "q")
	final(t, tm)
	saved := reload(t, d).Servers
	if len(saved) != 2 || saved[1].Jump != web.ID {
		t.Fatalf("saved %+v", saved)
	}
}

func TestDeleteJumpServerBlocked(t *testing.T) {
	d, _, _ := jumpDeps(t)
	tm := start(t, d)
	waitFor(t, tm, "web-1")
	press(tm, "d")
	waitFor(t, tm, "Delete web-1? (y/n)")
	press(tm, "y")
	waitFor(t, tm, "web-1 is the jump host for db-1. Change those first.")
	press(tm, "q")
	final(t, tm)
	if len(d.Inv.Servers) != 2 {
		t.Fatal("jump server deleted")
	}
}

func TestListShowsVia(t *testing.T) {
	d, _, _ := jumpDeps(t)
	tm := start(t, d)
	waitFor(t, tm, "admin@203.0.113.20 via web-1")
	press(tm, "q")
	final(t, tm)
}

func TestHostKeyPromptShowsVia(t *testing.T) {
	d, _, db := jumpDeps(t)
	m, _ := New(d).openPrompt(&sshx.Pending{Req: sshx.PromptRequest{Server: db, Kind: sshx.PromptHostKey, Detail: "SHA256:abc"}})
	if want := "New server db-1 (203.0.113.20:22 via web-1)"; !strings.Contains(m.viewPrompt(), want) {
		t.Fatalf("prompt lacks %q:\n%s", want, m.viewPrompt())
	}
}
