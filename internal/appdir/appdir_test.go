package appdir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := map[string]string{
		"~":            home,
		"~/.ssh/id":    filepath.Join(home, ".ssh", "id"),
		`~\.ssh\id`:    filepath.Join(home, ".ssh", "id"),
		"/etc/hosts":   "/etc/hosts",
		"relative/~/x": "relative/~/x",
		"":             "",
	}
	for in, want := range cases {
		if got := ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q)=%q want %q", in, got, want)
		}
	}
}

func TestWriteFileAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "servers.yaml")
	must(t, WriteFileAtomic(p, []byte("one"), 0600))
	must(t, WriteFileAtomic(p, []byte("two"), 0600))
	b, _ := os.ReadFile(p)
	if string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left: %v", entries)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("perm %v", fi.Mode().Perm())
		}
	}
}

func TestEnsureDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b")
	must(t, EnsureDir(p))
	fi, err := os.Stat(p)
	must(t, err)
	if !fi.IsDir() {
		t.Fatal("not a dir")
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0700 {
		t.Fatalf("perm %v", fi.Mode().Perm())
	}
}

func TestDirsAreNamedManagify(t *testing.T) {
	c, err := ConfigDir()
	must(t, err)
	d, err := DataDir()
	must(t, err)
	k, err := KnownHostsPath()
	must(t, err)
	if filepath.Base(c) != "managify" || filepath.Base(d) != "Managify" || filepath.Base(k) != "known_hosts" {
		t.Fatal(c, d, k)
	}
	if filepath.Base(filepath.Dir(k)) != ".ssh" {
		t.Fatal(k)
	}
}
