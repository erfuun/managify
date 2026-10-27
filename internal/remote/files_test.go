package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/sshx"
	"github.com/erfuun/managify/internal/testkit"
)

func TestResolveRemotePath(t *testing.T) {
	cases := []struct{ remote, base, home, want string }{
		{"/tmp/", "deploy.sh", "/home/u", "/tmp/deploy.sh"},
		{"~/bin/x.sh", "x.sh", "/home/u", "/home/u/bin/x.sh"},
		{"~", "a", "/home/u", "/home/u/a"},
		{"~/", "a", "/home/u", "/home/u/a"},
		{"~/bin/", "a", "/home/u", "/home/u/bin/a"},
		{"/opt/app.conf", "a", "/h", "/opt/app.conf"},
		{"", "a", "/home/u", "/home/u/a"},
		{"~/x", "a", "/", "/x"},
	}
	for _, c := range cases {
		if got := ResolveRemotePath(c.remote, c.base, c.home); got != c.want {
			t.Errorf("ResolveRemotePath(%q, %q, %q) = %q, want %q", c.remote, c.base, c.home, got, c.want)
		}
	}
}

// sftpServer has /tmp, as a real server would.
func sftpServer(t *testing.T) sshx.Client {
	t.Helper()
	c := testkit.Dial(t, testkit.Start(t, sshtest.Options{SFTP: true}))
	sc, err := c.NewSFTP()
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	if err := sc.Mkdir("/tmp"); err != nil {
		t.Fatal(err)
	}
	return c
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	c := sftpServer(t)
	data := make([]byte, 300*1024)
	rand.Read(data)
	local := writeTemp(t, "blob.bin", data)

	var last [2]int64
	calls := 0
	final, err := Upload(context.Background(), c, local, "/tmp/", func(done, total int64) {
		last = [2]int64{done, total}
		calls++
	})
	if err != nil || final != "/tmp/blob.bin" {
		t.Fatalf("Upload = %q, %v", final, err)
	}
	if n := int64(len(data)); last != [2]int64{n, n} || calls > len(data)/(64*1024)+2 {
		t.Fatalf("progress last=%v calls=%d", last, calls)
	}

	dir := filepath.Join(t.TempDir(), "downloads", "web-1")
	got, err := Download(context.Background(), c, final, dir, nil)
	if err != nil || got != filepath.Join(dir, "blob.bin") {
		t.Fatalf("Download = %q, %v", got, err)
	}
	b, _ := os.ReadFile(got)
	if !bytes.Equal(b, data) {
		t.Fatal("downloaded bytes differ")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(got)
		di, _ := os.Stat(dir)
		if fi.Mode().Perm() != 0600 || di.Mode().Perm() != 0700 {
			t.Fatalf("modes: file %v dir %v", fi.Mode(), di.Mode())
		}
	}
	if _, err := os.Stat(got + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary .part file left behind")
	}
}

func TestUploadOverwrites(t *testing.T) {
	c := sftpServer(t)
	if _, err := Upload(context.Background(), c, writeTemp(t, "x.conf", []byte("old content, longer")), "/tmp/x.conf", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Upload(context.Background(), c, writeTemp(t, "x.conf", []byte("new")), "/tmp/x.conf", nil); err != nil {
		t.Fatal(err)
	}
	got, err := Download(context.Background(), c, "/tmp/x.conf", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); string(b) != "new" {
		t.Fatalf("remote file = %q", b)
	}
}

func TestDownloadUnsafeRemoteName(t *testing.T) {
	c := sftpServer(t)
	if _, err := Upload(context.Background(), c, writeTemp(t, "a.log", []byte("x")), "/tmp/a:b.log", nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	got, err := Download(context.Background(), c, "/tmp/a:b.log", dir, nil)
	if err != nil || got != filepath.Join(dir, "a_b.log") {
		t.Fatalf("Download = %q, %v", got, err)
	}
}

func TestDownloadDirectoryRefused(t *testing.T) {
	c := sftpServer(t)
	dir := t.TempDir()
	if _, err := Download(context.Background(), c, "/tmp/", dir, nil); err == nil {
		t.Fatal("downloading a directory should fail")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left files: %v", entries)
	}
}

func TestUploadCancelled(t *testing.T) {
	c := sftpServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Upload(ctx, c, writeTemp(t, "a", []byte("x")), "/tmp/", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestHasCRLF(t *testing.T) {
	bin := append([]byte("MZ\x00\x00"), []byte("a\r\nb")...)
	cases := []struct {
		data []byte
		want bool
	}{
		{[]byte("a\r\nb\r\n"), true},
		{[]byte("a\nb\n"), false},
		{bin, false},
		{nil, false},
	}
	for _, c := range cases {
		got, err := HasCRLF(writeTemp(t, "f", c.data))
		if err != nil || got != c.want {
			t.Errorf("HasCRLF(%q) = %v, %v", c.data, got, err)
		}
	}
}

// Like scp: an existing directory without a trailing "/" is uploaded into.
func TestUploadIntoDirWithoutSlash(t *testing.T) {
	c := sftpServer(t)
	final, err := Upload(context.Background(), c, writeTemp(t, "blob.bin", []byte("x")), "/tmp", nil)
	if err != nil || final != "/tmp/blob.bin" {
		t.Fatalf("Upload = %q, %v", final, err)
	}
}
