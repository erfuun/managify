package sshtest

import (
	"errors"
	"io"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

// echoListener echoes every connection back until it closes.
func echoListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestForwardReachesTarget(t *testing.T) {
	s := Start(t, Options{Password: "pw", Forward: true})
	c, err := dial(t, s, ssh.Password("pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	conn, err := c.Dial("tcp", echoListener(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q %v", buf, err)
	}
}

func openErr(t *testing.T, o Options, addr string) *ssh.OpenChannelError {
	t.Helper()
	s := Start(t, o)
	c, err := dial(t, s, ssh.Password("pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Dial("tcp", addr)
	var oe *ssh.OpenChannelError
	if !errors.As(err, &oe) {
		t.Fatalf("err = %v, want *ssh.OpenChannelError", err)
	}
	return oe
}

func TestDenyForward(t *testing.T) {
	oe := openErr(t, Options{Password: "pw", DenyForward: true}, echoListener(t))
	if oe.Reason != ssh.Prohibited {
		t.Fatalf("reason %v", oe.Reason)
	}
}

func TestForwardRefused(t *testing.T) {
	oe := openErr(t, Options{Password: "pw", Forward: true}, closedPort(t))
	if oe.Reason != ssh.ConnectionFailed {
		t.Fatalf("reason %v (%s)", oe.Reason, oe.Message)
	}
}

func TestForwardOffByDefault(t *testing.T) {
	oe := openErr(t, Options{Password: "pw"}, echoListener(t))
	if oe.Reason != ssh.UnknownChannelType {
		t.Fatalf("reason %v", oe.Reason)
	}
}
