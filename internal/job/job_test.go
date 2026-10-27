package job

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/remote"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/sshx"
	"github.com/erfuun/managify/internal/testkit"
)

type events struct {
	mu  sync.Mutex
	got []Event
}

func (e *events) emit(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, ev)
}

func deps(t *testing.T, env *testkit.Env) Deps {
	return Deps{Pool: env.Pool, Creds: env.Creds, DataDir: t.TempDir(),
		Now: func() time.Time { return time.Date(2026, 10, 27, 14, 3, 5, 0, time.UTC) }}
}

// done checks EvDone is the last event and the only one, and returns it.
func done(t *testing.T, got []Event) Event {
	t.Helper()
	n := 0
	for _, ev := range got {
		if ev.Kind == EvDone {
			n++
		}
	}
	if n != 1 || got[len(got)-1].Kind != EvDone {
		t.Fatalf("want exactly one EvDone, last: %+v", got)
	}
	return got[len(got)-1]
}

func TestExecuteRunsAndLogs(t *testing.T) {
	env := testkit.NewEnv(t)
	srv := testkit.Start(t, sshtest.Options{Exec: func(_ context.Context, cmd string, _ io.Reader, stdout, _ io.Writer) int {
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(stdout, "line %d\n", i)
		}
		return 0
	}})
	s := env.PasswordServer(t, srv, "web-1")
	var ev events
	Execute(context.Background(), deps(t, env), Spec{Command: "seq 3"}, s, ev.emit)

	var lines []string
	for _, e := range ev.got {
		if e.Kind == EvLine {
			lines = append(lines, e.Line.Text)
		}
		if e.ServerID != s.ID {
			t.Fatalf("event for %q: %+v", e.ServerID, e)
		}
	}
	if strings.Join(lines, ",") != "line 1,line 2,line 3" {
		t.Fatalf("lines = %v", lines)
	}
	last := done(t, ev.got)
	if last.Result.ExitCode != 0 || last.Result.Err != nil || last.LogPath == "" {
		t.Fatalf("done = %+v", last)
	}
	b, err := os.ReadFile(last.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "# server: web-1 (") || !strings.Contains(string(b), "# command: seq 3\n# started: 2026-10-27T14:03:05Z\nline 1\nline 2\nline 3\n# exit: 0") {
		t.Fatalf("log =\n%s", b)
	}
}

const sudoPw = "s3cret"

// fakeSudo accepts sudoPw on stdin and prints "ok", else sudo's rejection.
func fakeSudo(_ context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) int {
	if !strings.HasPrefix(cmd, "sudo -S -p '' -- sh -c ") {
		fmt.Fprintf(stdout, "unexpected command %q", cmd)
		return 127
	}
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	if line != sudoPw+"\n" {
		fmt.Fprintln(stderr, "sudo: 1 incorrect password attempt")
		return 1
	}
	fmt.Fprintln(stdout, "ok")
	return 0
}

func TestExecuteSudoPasswordNeverLogged(t *testing.T) {
	env := testkit.NewEnv(t)
	srv := sshtest.Start(t, sshtest.Options{Password: sudoPw, Exec: fakeSudo})
	s := env.PasswordServer(t, srv, "web-1")
	env.Store.Set(secrets.LoginAccount(s.ID), sudoPw)
	var ev events
	Execute(context.Background(), deps(t, env), Spec{Command: "whoami", Sudo: true}, s, ev.emit)

	last := done(t, ev.got)
	if last.Result.ExitCode != 0 || last.Result.Err != nil {
		t.Fatalf("done = %+v (events %+v)", last, ev.got)
	}
	for _, e := range ev.got {
		if strings.Contains(e.Line.Text, sudoPw) || strings.Contains(e.Text, sudoPw) {
			t.Fatalf("password in event %+v", e)
		}
	}
	b, _ := os.ReadFile(last.LogPath)
	if strings.Contains(string(b), sudoPw) || !strings.Contains(string(b), "# sudo: yes\n# started: 2026-10-27T14:03:05Z\nok\n") {
		t.Fatalf("log =\n%s", b)
	}
}

// A rejected sudo password is forgotten, so the next run asks again.
func TestExecuteSudoRejectedForgetsPassword(t *testing.T) {
	env := testkit.NewEnv(t)
	signer, keyPath := sshtest.ClientKey(t)
	srv := sshtest.Start(t, sshtest.Options{AuthorizedKey: signer.PublicKey(), Exec: fakeSudo})
	env.Trust(t, srv)
	s := inventory.Server{ID: inventory.NewID(), Name: "web-1", Host: srv.Host, Port: srv.Port, User: "root",
		Auth: inventory.AuthKey, KeyPath: keyPath, SudoPassword: inventory.SudoAsk}
	env.Session.Set(secrets.SudoAccount(s.ID), "wrong")
	var ev events
	Execute(context.Background(), deps(t, env), Spec{Command: "whoami", Sudo: true}, s, ev.emit)

	if last := done(t, ev.got); !errors.Is(last.Result.Err, remote.ErrSudoRejected) {
		t.Fatalf("done = %+v", last)
	}
	if _, ok := env.Session.Get(secrets.SudoAccount(s.ID)); ok {
		t.Fatal("rejected sudo password still cached")
	}
}

func TestExecuteDialErrorStillDone(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	env := testkit.NewEnv(t)
	s := inventory.Server{ID: inventory.NewID(), Name: "gone", Host: "127.0.0.1", Port: port, User: "root", Auth: inventory.AuthPassword}
	var ev events
	Execute(context.Background(), deps(t, env), Spec{Command: "x"}, s, ev.emit)

	var ue *sshx.UnreachableError
	if len(ev.got) != 1 || !errors.As(done(t, ev.got).Result.Err, &ue) {
		t.Fatalf("events = %+v", ev.got)
	}
}

// Without a writable data dir the command still runs; the user is warned.
func TestExecuteLogFailureWarnsAndRuns(t *testing.T) {
	env := testkit.NewEnv(t)
	srv := testkit.Start(t, sshtest.Options{Exec: func(_ context.Context, _ string, _ io.Reader, stdout, _ io.Writer) int {
		fmt.Fprintln(stdout, "hi")
		return 0
	}})
	s := env.PasswordServer(t, srv, "web-1")
	d := deps(t, env)
	blocker := d.DataDir + "/outputs"
	if err := os.WriteFile(blocker, nil, 0600); err != nil { // a file where the folder should be
		t.Fatal(err)
	}
	var ev events
	Execute(context.Background(), d, Spec{Command: "echo hi"}, s, ev.emit)

	var warned, sawLine bool
	for _, e := range ev.got {
		warned = warned || (e.Kind == EvWarn && strings.Contains(e.Text, "log"))
		sawLine = sawLine || (e.Kind == EvLine && e.Line.Text == "hi")
	}
	last := done(t, ev.got)
	if !warned || !sawLine || last.LogPath != "" || last.Result.ExitCode != 0 {
		t.Fatalf("events = %+v", ev.got)
	}
}

// transferServer serves SFTP (with /tmp) and runs "bash /tmp/deploy.sh".
func transferServer(t *testing.T) *sshtest.Server {
	t.Helper()
	srv := testkit.Start(t, sshtest.Options{SFTP: true, Exec: func(_ context.Context, cmd string, _ io.Reader, stdout, _ io.Writer) int {
		if cmd != "bash /tmp/deploy.sh" {
			fmt.Fprintf(stdout, "unexpected command %q", cmd)
			return 127
		}
		fmt.Fprintln(stdout, "deployed")
		return 0
	}})
	sc, err := testkit.Dial(t, srv).NewSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	if err := sc.Mkdir("/tmp"); err != nil {
		t.Fatal(err)
	}
	return srv
}

func localFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// kinds lists event kinds with their text, for order checks.
func kinds(got []Event) []string {
	var out []string
	for _, e := range got {
		switch e.Kind {
		case EvLine:
			out = append(out, "line:"+e.Line.Text)
		case EvWarn:
			out = append(out, "warn")
		case EvUploaded:
			out = append(out, "uploaded:"+e.Text)
		case EvDownloaded:
			out = append(out, "downloaded")
		case EvDone:
			out = append(out, "done")
		}
	}
	return out
}

func TestUploadThenRun(t *testing.T) {
	env := testkit.NewEnv(t)
	s := env.PasswordServer(t, transferServer(t), "web-1")
	local := localFile(t, "deploy.sh", "echo deployed\n")
	var ev events
	Execute(context.Background(), deps(t, env), Spec{Upload: &Upload{Local: local, Remote: "/tmp/"}, Command: "bash /tmp/deploy.sh"}, s, ev.emit)

	if got := strings.Join(kinds(ev.got), ","); got != "uploaded:/tmp/deploy.sh,line:deployed,done" {
		t.Fatalf("events = %s", got)
	}
	last := done(t, ev.got)
	if last.Result.ExitCode != 0 || last.Result.Err != nil {
		t.Fatalf("done = %+v", last)
	}
	b, _ := os.ReadFile(last.LogPath)
	if !strings.Contains(string(b), "# upload: "+local+" -> /tmp/") || !strings.Contains(string(b), "# uploaded to /tmp/deploy.sh\n") {
		t.Fatalf("log =\n%s", b)
	}
	var sawProgress bool
	for _, e := range ev.got {
		sawProgress = sawProgress || (e.Kind == EvProgress && e.Progress == 1)
	}
	if !sawProgress {
		t.Fatal("no final progress event")
	}
}

func TestUploadOnlyIsDone(t *testing.T) {
	env := testkit.NewEnv(t)
	s := env.PasswordServer(t, transferServer(t), "web-1")
	var ev events
	Execute(context.Background(), deps(t, env), Spec{Upload: &Upload{Local: localFile(t, "app.conf", "x"), Remote: "/tmp/"}}, s, ev.emit)
	if got := strings.Join(kinds(ev.got), ","); got != "uploaded:/tmp/app.conf,done" || done(t, ev.got).Result.ExitCode != 0 {
		t.Fatalf("events = %s", got)
	}
}

func TestUploadCRLFWarns(t *testing.T) {
	env := testkit.NewEnv(t)
	s := env.PasswordServer(t, transferServer(t), "web-1")
	var ev events
	Execute(context.Background(), deps(t, env), Spec{Upload: &Upload{Local: localFile(t, "deploy.sh", "echo a\r\necho b\r\n"), Remote: "/tmp/"}}, s, ev.emit)
	const want = `warning: deploy.sh has Windows line endings (CRLF); shell scripts may fail with $'\r': command not found`
	if k := kinds(ev.got); len(k) < 2 || k[0] != "warn" || k[1] != "uploaded:/tmp/deploy.sh" {
		t.Fatalf("events = %v", k)
	}
	for _, e := range ev.got {
		if e.Kind == EvWarn && e.Text != want {
			t.Fatalf("warning = %q", e.Text)
		}
	}
}

func TestUploadFailureIsDone(t *testing.T) {
	env := testkit.NewEnv(t)
	s := env.PasswordServer(t, transferServer(t), "web-1")
	var ev events
	Execute(context.Background(), deps(t, env), Spec{Upload: &Upload{Local: localFile(t, "a", "x"), Remote: "/nope/dir/a"}, Command: "bash /tmp/deploy.sh"}, s, ev.emit)
	last := done(t, ev.got)
	if last.Result.Err == nil || strings.Contains(strings.Join(kinds(ev.got), ","), "line:") {
		t.Fatalf("events = %v, done = %+v", kinds(ev.got), last)
	}
}

func TestDownloadToSafeDir(t *testing.T) {
	env := testkit.NewEnv(t)
	srv := transferServer(t)
	s := env.PasswordServer(t, srv, "web:1")
	if _, err := remote.Upload(context.Background(), testkit.Dial(t, srv), localFile(t, "syslog", "boot ok\n"), "/tmp/", nil); err != nil {
		t.Fatal(err)
	}
	d := deps(t, env)
	var ev events
	Execute(context.Background(), d, Spec{Download: "/tmp/syslog"}, s, ev.emit)

	want := filepath.Join(d.DataDir, "downloads", "web_1", "syslog")
	var got string
	for _, e := range ev.got {
		if e.Kind == EvDownloaded {
			got = e.Text
		}
	}
	if got != want || done(t, ev.got).Result.Err != nil {
		t.Fatalf("downloaded to %q, events %+v", got, ev.got)
	}
	if b, _ := os.ReadFile(want); string(b) != "boot ok\n" {
		t.Fatalf("file = %q", b)
	}
}
