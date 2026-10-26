package sshtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func dial(t *testing.T, s *Server, auth ...ssh.AuthMethod) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", s.Addr, &ssh.ClientConfig{
		User:            "root",
		Auth:            auth,
		HostKeyCallback: ssh.FixedHostKey(s.HostSigner.PublicKey()),
		Timeout:         5 * time.Second,
	})
}

func echoExec(ctx context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "ran: %s", cmd)
	return 0
}

func TestPasswordAuth(t *testing.T) {
	s := Start(t, Options{Password: "hunter2", Exec: echoExec})
	c, err := dial(t, s, ssh.Password("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sess, _ := c.NewSession()
	out, err := sess.Output("echo hi")
	if err != nil || string(out) != "ran: echo hi" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := dial(t, s, ssh.Password("wrong")); err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestKeyboardInteractiveAuth(t *testing.T) {
	s := Start(t, Options{Password: "hunter2", Exec: echoExec})
	ki := ssh.KeyboardInteractive(func(name, instr string, qs []string, echos []bool) ([]string, error) {
		ans := make([]string, len(qs))
		for i := range ans {
			ans[i] = "hunter2"
		}
		return ans, nil
	})
	c, err := dial(t, s, ki)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestKeyAuth(t *testing.T) {
	signer, path := ClientKey(t)
	if path == "" {
		t.Fatal("no key path")
	}
	s := Start(t, Options{AuthorizedKey: signer.PublicKey(), Exec: echoExec})
	c, err := dial(t, s, ssh.PublicKeys(signer))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	other, _ := ClientKey(t)
	if _, err := dial(t, s, ssh.PublicKeys(other)); err == nil {
		t.Fatal("other key accepted")
	}
}

func TestEncryptedClientKey(t *testing.T) {
	_, path := EncryptedClientKey(t, "pass")
	pemBytes := mustRead(t, path)
	if _, err := ssh.ParsePrivateKey(pemBytes); err == nil {
		t.Fatal("key is not encrypted")
	}
	if _, err := ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte("pass")); err != nil {
		t.Fatal(err)
	}
}

func TestExecExitCode(t *testing.T) {
	s := Start(t, Options{Password: "p", Exec: func(ctx context.Context, cmd string, _ io.Reader, _, _ io.Writer) int { return 3 }})
	c, _ := dial(t, s, ssh.Password("p"))
	defer c.Close()
	sess, _ := c.NewSession()
	err := sess.Run("x")
	var ee *ssh.ExitError
	if !errors.As(err, &ee) || ee.ExitStatus() != 3 {
		t.Fatalf("err = %v", err)
	}
}

func TestSignalRecorded(t *testing.T) {
	stopped := make(chan struct{})
	s := Start(t, Options{Password: "p", Exec: func(ctx context.Context, cmd string, _ io.Reader, _, _ io.Writer) int {
		<-ctx.Done()
		close(stopped)
		return 130
	}})
	c, _ := dial(t, s, ssh.Password("p"))
	defer c.Close()
	sess, _ := c.NewSession()
	if err := sess.Start("sleep"); err != nil {
		t.Fatal(err)
	}
	if err := sess.Signal(ssh.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("exec ctx not cancelled by signal")
	}
	if got := s.Signals(); len(got) != 1 || got[0] != "INT" {
		t.Fatalf("signals = %v", got)
	}
}

func TestShellRecordsPty(t *testing.T) {
	s := Start(t, Options{Password: "p", Shell: true})
	c, _ := dial(t, s, ssh.Password("p"))
	defer c.Close()
	sess, _ := c.NewSession()
	if err := sess.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	sess.Stdin = strings.NewReader("echo hi\nexit\n")
	var out strings.Builder
	sess.Stdout = &out
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hi") {
		t.Fatalf("out = %q", out.String())
	}
	if s.PtyTerm() != "xterm-256color" {
		t.Fatalf("term = %q", s.PtyTerm())
	}
	if sz := s.WindowSizes(); len(sz) != 1 || sz[0] != [2]int{80, 24} {
		t.Fatalf("sizes = %v", sz)
	}
}

func TestSFTPRoundTrip(t *testing.T) {
	s := Start(t, Options{Password: "p", SFTP: true})
	c, _ := dial(t, s, ssh.Password("p"))
	defer c.Close()
	write, err := sftp.NewClient(c)
	if err != nil {
		t.Fatal(err)
	}
	write.Mkdir("/tmp")
	f, err := write.Create("/tmp/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("hello"))
	f.Close()
	write.Close()

	// A second SFTP session sees the same files.
	read, _ := sftp.NewClient(c)
	defer read.Close()
	g, err := read.Open("/tmp/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(g)
	if string(b) != "hello" {
		t.Fatalf("read %q", b)
	}
}

func TestConnsAndDropAll(t *testing.T) {
	s := Start(t, Options{Password: "p", Exec: echoExec})
	c1, _ := dial(t, s, ssh.Password("p"))
	c2, _ := dial(t, s, ssh.Password("p"))
	if s.Conns() != 2 {
		t.Fatalf("conns = %d", s.Conns())
	}
	s.DropAll()
	done := make(chan error, 2)
	go func() { done <- c1.Wait() }()
	go func() { done <- c2.Wait() }()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("client not disconnected")
		}
	}
}

func TestKnownHostsLine(t *testing.T) {
	s := Start(t, Options{Password: "p"})
	line := s.KnownHostsLine()
	if !strings.HasPrefix(line, "[127.0.0.1]:") || !strings.Contains(line, "ssh-ed25519 ") {
		t.Fatalf("line = %q", line)
	}
}
