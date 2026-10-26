package sshx

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"managify/internal/sshtest"
)

func hkServer(t *testing.T) *sshtest.Server {
	return sshtest.Start(t, sshtest.Options{Password: "p"})
}

func dialWith(t *testing.T, srv *sshtest.Server, path string, p Prompter) error {
	t.Helper()
	cb, err := HostKeyCallback(context.Background(), path, pwServer, p)
	if err != nil {
		return err
	}
	c, err := ssh.Dial("tcp", srv.Addr, &ssh.ClientConfig{
		User: "root", Auth: []ssh.AuthMethod{ssh.Password("p")},
		HostKeyCallback: cb, Timeout: 5 * time.Second,
	})
	if err == nil {
		c.Close()
	}
	return err
}

func randomKey(t *testing.T) ssh.PublicKey {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func khPath(t *testing.T) string { return filepath.Join(t.TempDir(), ".ssh", "known_hosts") }

func TestCreatesMissingKnownHosts(t *testing.T) {
	path := khPath(t)
	if _, err := HostKeyCallback(context.Background(), path, pwServer, &fakePrompter{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("known_hosts not created: %v", err)
	}
}

func TestUnknownHostPromptsAndRemembers(t *testing.T) {
	srv, path := hkServer(t), khPath(t)
	p := &fakePrompter{}
	for i := 0; i < 2; i++ {
		if err := dialWith(t, srv, path, p); err != nil {
			t.Fatal(err)
		}
	}
	if p.count(PromptHostKey) != 1 {
		t.Fatalf("host-key prompts = %d", p.count(PromptHostKey))
	}
	if want := ssh.FingerprintSHA256(srv.HostSigner.PublicKey()); p.Last.Detail != want {
		t.Fatalf("detail = %q, want %q", p.Last.Detail, want)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), srv.KnownHostsLine()) {
		t.Fatalf("known_hosts = %q", b)
	}
}

func TestUnknownHostDeclined(t *testing.T) {
	srv, path := hkServer(t), khPath(t)
	err := dialWith(t, srv, path, &fakePrompter{Err: ErrPromptCancelled})
	if !errors.Is(err, ErrHostKeyRejected) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Fatalf("known_hosts written: %q", b)
	}
}

func TestMismatchBlocks(t *testing.T) {
	srv, path := hkServer(t), khPath(t)
	os.MkdirAll(filepath.Dir(path), 0700)
	other := knownhosts.Line([]string{knownhosts.Normalize(srv.Addr)}, randomKey(t))
	os.WriteFile(path, []byte(other+"\n"), 0600)
	p := &fakePrompter{}
	err := dialWith(t, srv, path, p)
	var mm *HostKeyMismatchError
	if !errors.As(err, &mm) || mm.KnownHostsPath != path || mm.Server.Name != pwServer.Name {
		t.Fatalf("err = %v", err)
	}
	if p.count(PromptHostKey) != 0 {
		t.Fatal("prompted on a changed key")
	}
	if b, _ := os.ReadFile(path); string(b) != other+"\n" {
		t.Fatal("known_hosts changed")
	}
}

func TestAppendAfterFileWithoutTrailingNewline(t *testing.T) {
	srv, path := hkServer(t), khPath(t)
	os.MkdirAll(filepath.Dir(path), 0700)
	existing := knownhosts.Line([]string{"example.com"}, randomKey(t))
	os.WriteFile(path, []byte(existing), 0600) // no trailing newline
	if err := dialWith(t, srv, path, &fakePrompter{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := existing + "\n" + srv.KnownHostsLine() + "\n"
	if string(b) != want {
		t.Fatalf("known_hosts =\n%q\nwant\n%q", b, want)
	}
	if _, err := knownhosts.New(path); err != nil {
		t.Fatal(err)
	}
}

func TestUnparseableKnownHostsIsLeftAlone(t *testing.T) {
	path := khPath(t)
	os.MkdirAll(filepath.Dir(path), 0700)
	bad := []byte("garbage line here\n")
	os.WriteFile(path, bad, 0600)
	_, err := HostKeyCallback(context.Background(), path, pwServer, &fakePrompter{})
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(bad) {
		t.Fatal("known_hosts rewritten")
	}
}
