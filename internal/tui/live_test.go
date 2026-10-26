package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/remote"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

const probeFixture = `0.42 0.30 0.25 1/123 4567
MemTotal:        2048000 kB
MemAvailable:    1208320 kB
Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/vda1         41152736 25600000  13456000      63% /
`

func probeExec(ctx context.Context, cmd string, _ io.Reader, stdout, _ io.Writer) int {
	if cmd != remote.ProbeCommand {
		fmt.Fprintf(stdout, "unexpected command %q", cmd)
		return 127
	}
	io.WriteString(stdout, probeFixture)
	return 0
}

func probeServer(t *testing.T) *sshtest.Server {
	return testkit.Start(t, sshtest.Options{Exec: probeExec})
}

// liveDeps is testDeps wired to a real connection stack.
func liveDeps(t *testing.T, interval time.Duration) (Deps, *testkit.Env) {
	t.Helper()
	env := testkit.NewEnv(t)
	d := testDeps(t)
	d.Secrets, d.Session = env.Store, env.Session
	d.Pool, d.Creds, d.Prompts = env.Pool, env.Creds, env.Prompts
	d.ProbeInterval = interval
	return d, env
}

type liveOpt int

const (
	savedPassword liveOpt = 1 << iota // testkit.Password in the keychain
	trusted                           // host key already in known_hosts
)

// addLive adds a password profile for srv. The secret is stored under the ID
// the inventory assigned, which is the one the dialer looks up.
func addLive(t *testing.T, d Deps, env *testkit.Env, srv *sshtest.Server, name string, o liveOpt) inventory.Server {
	t.Helper()
	s, err := d.Inv.Add(inventory.Server{
		Name: name, Host: srv.Host, Port: srv.Port, User: "root",
		Auth: inventory.AuthPassword, SavePassword: o&savedPassword != 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o&savedPassword != 0 {
		env.Store.Set(secrets.LoginAccount(s.ID), testkit.Password)
	}
	if o&trusted != 0 {
		env.Trust(t, srv)
	}
	return s
}

// collect reads output for d and returns everything that arrived.
func collect(tm *teatest.TestModel, d time.Duration) []byte {
	var out bytes.Buffer
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		io.Copy(&out, tm.Output())
		time.Sleep(20 * time.Millisecond)
	}
	return out.Bytes()
}

// waitForRe waits until the accumulated output matches every pattern.
func waitForRe(t *testing.T, tm *teatest.TestModel, patterns ...string) {
	t.Helper()
	res := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		res[i] = regexp.MustCompile(p)
	}
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, re := range res {
			if !re.Match(b) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(20*time.Millisecond))
}
