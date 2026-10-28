package outputs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/remote"
)

var (
	web1    = inventory.Server{ID: "7f3a9c21", Name: "web-1", Host: "203.0.113.10", Port: 22, User: "root"}
	started = time.Date(2026, 10, 27, 14, 3, 5, 0, time.FixedZone("", 3.5*3600))
)

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"web:1": "web_1", "a/b": "a_b", `a\b`: "a_b", "con": "con_", "COM1": "COM1_", "nul.txt": "nul_.txt", "aux.prod": "aux_.prod",
		"name. ": "name", "": "server", "...": "server", "tab\there": "tab_here", `<>"|?*`: "______", "web-1": "web-1",
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func readLog(t *testing.T, l *Log) string {
	t.Helper()
	b, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLogFormat(t *testing.T) {
	dir := t.TempDir()
	l, err := Begin(dir, web1, Header{Command: "df -h", Sudo: true, Upload: `C:\work\deploy.sh -> /tmp/deploy.sh`, Download: "/var/log/syslog"}, started)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "outputs", "web-1", "20261027-140305.log"); l.Path() != want {
		t.Fatalf("path = %s, want %s", l.Path(), want)
	}
	l.Line(remote.Line{Stream: remote.Stdout, Text: "Filesystem Size"})
	l.Line(remote.Line{Stream: remote.Stderr, Text: "df: /mnt: Permission denied"})
	l.Note("uploaded 1 file")
	if err := l.Finish(remote.Result{ExitCode: 0, Duration: 1234 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	want := `# server: web-1 (root@203.0.113.10:22)
# command: df -h
# sudo: yes
# upload: C:\work\deploy.sh -> /tmp/deploy.sh
# download: /var/log/syslog
# started: 2026-10-27T14:03:05+03:30
Filesystem Size
[err] df: /mnt: Permission denied
# uploaded 1 file
# exit: 0  duration: 1.2s
`
	if got := readLog(t, l); got != want {
		t.Fatalf("log =\n%s\nwant\n%s", got, want)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(l.Path())
		di, _ := os.Stat(filepath.Dir(l.Path()))
		if fi.Mode().Perm() != 0600 || di.Mode().Perm() != 0700 {
			t.Fatalf("modes: file %v dir %v", fi.Mode(), di.Mode())
		}
	}
}

func TestLogOptionalHeaderLines(t *testing.T) {
	l, err := Begin(t.TempDir(), web1, Header{Command: "uptime"}, started)
	if err != nil {
		t.Fatal(err)
	}
	l.Finish(remote.Result{ExitCode: 3, Duration: 40 * time.Millisecond})
	got := readLog(t, l)
	if strings.Contains(got, "# sudo:") || strings.Contains(got, "# upload:") || strings.Contains(got, "# download:") ||
		!strings.HasSuffix(got, "# exit: 3  duration: 0s\n") {
		t.Fatalf("log =\n%s", got)
	}
}

func TestLogCollision(t *testing.T) {
	dir := t.TempDir()
	a, err := Begin(dir, web1, Header{Command: "a"}, started)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Begin(dir, web1, Header{Command: "b"}, started)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Begin(dir, web1, Header{Command: "c"}, started)
	if err != nil {
		t.Fatal(err)
	}
	for l, want := range map[*Log]string{a: "20261027-140305.log", b: "20261027-140305-2.log", c: "20261027-140305-3.log"} {
		if filepath.Base(l.Path()) != want {
			t.Errorf("path = %s, want %s", l.Path(), want)
		}
		l.Finish(remote.Result{})
	}
}

func TestLogCancelledFooter(t *testing.T) {
	l, _ := Begin(t.TempDir(), web1, Header{Command: "sleep 9"}, started)
	l.Finish(remote.Result{ExitCode: -1, Cancelled: true, Duration: 3420 * time.Millisecond})
	if got := readLog(t, l); !strings.HasSuffix(got, "# cancelled after 3.4s (process may still be running)\n") {
		t.Fatalf("log =\n%s", got)
	}
}

func TestLogErrorFooter(t *testing.T) {
	l, _ := Begin(t.TempDir(), web1, Header{Command: "x"}, started)
	l.Finish(remote.Result{ExitCode: -1, Err: errors.New("connection lost")})
	if got := readLog(t, l); !strings.HasSuffix(got, "# error: connection lost\n") {
		t.Fatalf("log =\n%s", got)
	}
}

func TestLogUnsafeServerName(t *testing.T) {
	s := web1
	s.Name = "web:1/a"
	l, err := Begin(t.TempDir(), s, Header{Command: "x"}, started)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Finish(remote.Result{})
	if got := filepath.Base(filepath.Dir(l.Path())); got != "web_1_a" {
		t.Fatalf("dir = %s", got)
	}
}

// Every SafeName result must work as a real folder on this OS.
func TestSafeNameRoundTrip(t *testing.T) {
	root := t.TempDir()
	for _, in := range []string{"con", "CON.lan", "aux.prod", "nul.txt", "COM1", "COM0", "LPT0", "lpt9.x", "web:1", "a. ", `x\y/z`} {
		dir := filepath.Join(root, SafeName(in))
		p := filepath.Join(dir, "x.log")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Errorf("%q → %q: %v", in, SafeName(in), err)
			continue
		}
		if err := os.WriteFile(p, []byte(in), 0600); err != nil {
			t.Errorf("%q → %q: %v", in, SafeName(in), err)
			continue
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != in {
			t.Errorf("%q → %q: read back %q, %v", in, SafeName(in), b, err)
		}
	}
}

func TestLogMultilineCommand(t *testing.T) {
	l, _ := Begin(t.TempDir(), web1, Header{Command: "a\nb"}, started)
	l.Note("one\ntwo")
	l.Finish(remote.Result{})
	got := readLog(t, l)
	if !strings.Contains(got, "# command: a\n#   b\n") || !strings.Contains(got, "# one\n#   two\n") {
		t.Fatalf("log =\n%s", got)
	}
}

func TestLogHeaderVia(t *testing.T) {
	db := inventory.Server{Name: "db-1", Host: "10.0.0.12", Port: 22, User: "postgres"}
	l, err := Begin(t.TempDir(), db, Header{Command: "true", Via: "web-1"}, started)
	if err != nil {
		t.Fatal(err)
	}
	l.Finish(remote.Result{})
	if got := readLog(t, l); !strings.HasPrefix(got, "# server: db-1 (postgres@10.0.0.12:22 via web-1)\n") {
		t.Fatalf("log =\n%s", got)
	}
}
