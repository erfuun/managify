package tui

import "testing"

func TestListShowsServers(t *testing.T) {
	tm := start(t, testDeps(t, webServer, dbServer))
	waitFor(t, tm, "Managify", "web-1", "root@203.0.113.10", "db-1", "admin@203.0.113.20", "space select")
	press(tm, "q")
	final(t, tm)
}

func TestEmptyListHint(t *testing.T) {
	tm := start(t, testDeps(t))
	waitFor(t, tm, "No servers yet. Press a to add one.")
	press(tm, "q")
	if got := final(t, tm).Targets(); len(got) != 0 {
		t.Fatalf("targets on empty list: %v", got)
	}
}

func TestSelectionTargets(t *testing.T) {
	tm := start(t, testDeps(t, webServer, dbServer))
	waitFor(t, tm, "db-1")
	press(tm, "j", "space", "q")
	got := final(t, tm).Targets()
	if len(got) != 1 || got[0].Name != "db-1" {
		t.Fatalf("targets = %v", got)
	}
}

func TestSelectionInListOrder(t *testing.T) {
	tm := start(t, testDeps(t, webServer, dbServer))
	waitFor(t, tm, "db-1")
	press(tm, "j", "space", "k", "space", "q")
	got := final(t, tm).Targets()
	if len(got) != 2 || got[0].Name != "web-1" || got[1].Name != "db-1" {
		t.Fatalf("targets = %v", got)
	}
}

func TestCursorIsTargetWithoutSelection(t *testing.T) {
	tm := start(t, testDeps(t, webServer, dbServer))
	waitFor(t, tm, "db-1")
	press(tm, "down", "q")
	got := final(t, tm).Targets()
	if len(got) != 1 || got[0].Name != "db-1" {
		t.Fatalf("targets = %v", got)
	}
}

func TestHelpToggle(t *testing.T) {
	tm := start(t, testDeps(t, webServer))
	waitFor(t, tm, "web-1")
	press(tm, "?")
	waitFor(t, tm, "Keys", "refresh status")
	press(tm, "q")
	final(t, tm)
}
