package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

// fakeHome points the user's home (where the file picker starts) at a temp
// dir holding the given files.
func fakeHome(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// sftpLive starts an SFTP server that also runs echo commands, with the
// given dirs created.
func sftpLive(t *testing.T, dirs ...string) *sshtest.Server {
	t.Helper()
	srv := testkit.Start(t, sshtest.Options{SFTP: true, Exec: runExec(echoExec)})
	sc, err := testkit.Dial(t, srv).NewSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	for _, dir := range dirs {
		if err := sc.Mkdir(dir); err != nil {
			t.Fatal(err)
		}
	}
	return srv
}

func TestUploadRemembersRemoteDir(t *testing.T) {
	fakeHome(t, map[string]string{"deploy.sh": "echo hi\n"})
	d, env := liveDeps(t, 0)
	srv := sftpLive(t, "/tmp", "/opt")
	addLive(t, d, env, srv, "s-1", savedPassword|trusted)
	tm := start(t, d)
	waitFor(t, tm, "● up")

	press(tm, "u")
	waitFor(t, tm, "Upload to s-1", "deploy.sh")
	press(tm, "enter") // pick the only file
	waitFor(t, tm, "Remote path", "/tmp/")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlU})
	typeText(tm, "/opt/")
	press(tm, "enter")
	waitFor(t, tm, "Then run (optional)")
	press(tm, "enter")
	waitFor(t, tm, "s-1 ✓ 0", "uploading deploy.sh 100%", "uploaded to /opt/deploy.sh")
	press(tm, "esc")
	waitFor(t, tm, "space select")

	b, _ := os.ReadFile(invPath(t, d))
	if !strings.Contains(string(b), "last_remote_dir: /opt/") {
		t.Fatalf("servers.yaml =\n%s", b)
	}
	press(tm, "u")
	waitFor(t, tm, "deploy.sh")
	press(tm, "enter")
	waitFor(t, tm, "/opt/")
	press(tm, "esc", "esc", "q")
	tm.Send(tea.QuitMsg{})
	if m := final(t, tm); m.upload.remote.Value() != "/opt/" {
		t.Fatalf("prefill = %q", m.upload.remote.Value())
	}
}

func TestUploadThenRunShowsOutputAndWarning(t *testing.T) {
	fakeHome(t, map[string]string{"x.sh": "echo a\r\n"})
	d, env := liveDeps(t, 0)
	srv := sftpLive(t, "/tmp")
	addLive(t, d, env, srv, "s-1", savedPassword|trusted)
	tm := start(t, d)
	waitFor(t, tm, "● up")
	press(tm, "u")
	waitFor(t, tm, "x.sh")
	press(tm, "enter")
	waitFor(t, tm, "Remote path")
	press(tm, "enter")
	waitFor(t, tm, "Then run (optional)")
	typeText(tm, "echo ran")
	press(tm, "enter")
	waitFor(t, tm, "has Windows line endings (CRLF)", "uploaded to /tmp/x.sh", "s-1 ✓ 0")
	press(tm, "esc", "q")
	m := final(t, tm)
	if !strings.Contains(paneText(m, 0), "ran") {
		t.Fatalf("pane = %q", paneText(m, 0))
	}
}

func TestUploadEscCancels(t *testing.T) {
	fakeHome(t, map[string]string{"a.txt": "x"})
	d, env := liveDeps(t, 0)
	addLive(t, d, env, sftpLive(t), "s-1", savedPassword|trusted)
	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "u")
	waitFor(t, tm, "a.txt")
	press(tm, "esc")
	waitFor(t, tm, "space select")
	press(tm, "q")
	if m := final(t, tm); m.run != nil {
		t.Fatal("esc in the picker started something")
	}
}

func TestDownload(t *testing.T) {
	fakeHome(t, nil)
	d, env := liveDeps(t, 0)
	srv := sftpLive(t, "/tmp")
	addLive(t, d, env, srv, "s-1", savedPassword|trusted)
	sc, _ := testkit.Dial(t, srv).NewSFTP()
	f, _ := sc.Create("/tmp/syslog")
	f.Write([]byte("boot ok\n"))
	f.Close()
	sc.Close()

	tm := start(t, d)
	waitFor(t, tm, "s-1")
	press(tm, "g")
	waitFor(t, tm, "Download from s-1")
	typeText(tm, "/tmp/syslog")
	press(tm, "enter")
	want := filepath.Join(d.DataDir, "downloads", "s-1", "syslog")
	waitFor(t, tm, "s-1 ✓ 0", "downloaded to")
	press(tm, "esc", "q")
	final(t, tm)
	if b, _ := os.ReadFile(want); string(b) != "boot ok\n" {
		t.Fatalf("%s = %q", want, b)
	}
}

func TestProgressReplacesItsLine(t *testing.T) {
	p := &pane{prog: -1}
	p.add("before")
	for _, f := range []float64{0.25, 0.5, 1} {
		p.progress("uploading x", f)
	}
	if got := strings.Join(p.lines, "|"); got != "before|uploading x 100%" {
		t.Fatalf("lines = %q", got)
	}
	p.progress("downloading y", 0.5)
	if n := len(p.lines); n != 3 || p.lines[2] != "downloading y 50%" {
		t.Fatalf("a new label adds a line: %q", p.lines)
	}
}

func TestProgressSurvivesTrim(t *testing.T) {
	p := &pane{prog: -1}
	for range maxPaneLines {
		p.add("old")
	}
	p.progress("uploading x", 0.1)
	for range 1000 {
		p.add("new")
	}
	p.progress("uploading x", 0.9)
	if p.prog < 0 || p.lines[p.prog] != "uploading x 90%" {
		t.Fatalf("prog = %d", p.prog)
	}
	if n := strings.Count(strings.Join(p.lines, "\n"), "uploading x"); n != 1 {
		t.Fatalf("%d progress lines", n)
	}
}
