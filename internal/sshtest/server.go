// Package sshtest runs an in-process SSH + SFTP server for tests, so no test
// ever needs Docker or a real machine. Import it only from _test.go files.
package sshtest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ExecFunc handles one "exec" request and returns its exit code. ctx is
// cancelled when the client sends a signal or closes the channel.
type ExecFunc func(ctx context.Context, cmd string, stdin io.Reader, stdout, stderr io.Writer) int

type Options struct {
	Password      string        // "" disables password and keyboard-interactive auth
	AuthorizedKey ssh.PublicKey // nil disables key auth
	Exec          ExecFunc
	Shell         bool // accept pty-req/shell; echo each line, exit 0 on "exit"
	SFTP          bool // "sftp" subsystem backed by one in-memory filesystem
	ECDSA         bool // also offer an ECDSA P-256 host key, like real servers offering several types
	// IgnoreGlobalRequests never answers global requests such as keepalives,
	// like a half-open connection after the network silently went away.
	IgnoreGlobalRequests bool
	// PublicKeyAlgorithms limits key-auth signature algorithms, e.g. to refuse
	// SHA-1 "ssh-rsa" like OpenSSH 8.8+. nil keeps x/crypto's defaults.
	PublicKeyAlgorithms []string
	// Forward accepts direct-tcpip channels, like a jump host; DenyForward
	// rejects them as "administratively prohibited", like AllowTcpForwarding no.
	Forward     bool
	DenyForward bool
}

type Server struct {
	Addr        string
	Host        string
	Port        int
	HostSigner  ssh.Signer
	ECDSASigner ssh.Signer // nil unless Options.ECDSA

	opts   Options
	cfg    *ssh.ServerConfig
	ln     net.Listener
	ctx    context.Context
	cancel context.CancelFunc
	fs     sftp.Handlers

	mu    sync.Mutex
	live  map[net.Conn]struct{}
	conns int
	// handshaking counts server-side handshakes not yet finished. A client's
	// handshake can return a moment before the server's, so Conns waits for
	// these to settle.
	handshaking int
	signals     []string
	ptyTerm     string
	sizes       [][2]int
}

// Start listens on 127.0.0.1 (never all interfaces, so no firewall prompts)
// and shuts down in t.Cleanup without waiting for handlers.
func Start(t testing.TB, o Options) *Server {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		Addr:       ln.Addr().String(),
		Host:       "127.0.0.1",
		Port:       ln.Addr().(*net.TCPAddr).Port,
		HostSigner: hostSigner,
		opts:       o,
		ln:         ln,
		ctx:        ctx,
		cancel:     cancel,
		fs:         sftp.InMemHandler(),
		live:       map[net.Conn]struct{}{},
	}
	if o.ECDSA {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if s.ECDSASigner, err = ssh.NewSignerFromKey(k); err != nil {
			t.Fatal(err)
		}
	}
	s.cfg = s.serverConfig()
	go s.acceptLoop()
	t.Cleanup(func() {
		cancel()
		ln.Close()
		s.DropAll()
	})
	return s
}

func (s *Server) serverConfig() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{PublicKeyAuthAlgorithms: s.opts.PublicKeyAlgorithms}
	if pw := s.opts.Password; pw != "" {
		cfg.PasswordCallback = func(_ ssh.ConnMetadata, got []byte) (*ssh.Permissions, error) {
			if string(got) == pw {
				return nil, nil
			}
			return nil, fmt.Errorf("wrong password")
		}
		cfg.KeyboardInteractiveCallback = func(_ ssh.ConnMetadata, ask ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			ans, err := ask("", "", []string{"Password: "}, []bool{false})
			if err == nil && len(ans) == 1 && ans[0] == pw {
				return nil, nil
			}
			return nil, fmt.Errorf("wrong password")
		}
	}
	if k := s.opts.AuthorizedKey; k != nil {
		cfg.PublicKeyCallback = func(_ ssh.ConnMetadata, got ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(got.Marshal(), k.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("unknown key")
		}
	}
	cfg.AddHostKey(s.HostSigner)
	if s.ECDSASigner != nil {
		cfg.AddHostKey(s.ECDSASigner)
	}
	return cfg
}

func (s *Server) acceptLoop() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(c)
	}
}

func (s *Server) handleConn(c net.Conn) {
	s.mu.Lock()
	s.live[c] = struct{}{}
	s.handshaking++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.live, c)
		s.mu.Unlock()
		c.Close()
	}()
	sc, chans, reqs, err := ssh.NewServerConn(c, s.cfg)
	s.mu.Lock()
	s.handshaking--
	if err == nil {
		s.conns++
	}
	s.mu.Unlock()
	if err != nil {
		return
	}
	defer sc.Close()
	if s.opts.IgnoreGlobalRequests {
		go func() {
			for range reqs {
			}
		}()
	} else {
		go ssh.DiscardRequests(reqs)
	}
	for nc := range chans {
		if nc.ChannelType() == "direct-tcpip" && (s.opts.Forward || s.opts.DenyForward) {
			go s.handleForward(nc)
			continue
		}
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(ch, chReqs)
	}
}

// handleForward dials the address in a direct-tcpip request (RFC 4254 7.2)
// and copies both ways until either side closes.
func (s *Server) handleForward(nc ssh.NewChannel) {
	if s.opts.DenyForward {
		nc.Reject(ssh.Prohibited, "administratively prohibited")
		return
	}
	var req struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &req); err != nil {
		nc.Reject(ssh.ConnectionFailed, "bad request")
		return
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	target, err := d.DialContext(s.ctx, "tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))))
	if err != nil {
		nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		target.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	stop := context.AfterFunc(s.ctx, func() { ch.Close(); target.Close() })
	defer stop()
	done := make(chan struct{}, 2)
	go func() { io.Copy(target, ch); done <- struct{}{} }()
	go func() { io.Copy(ch, target); done <- struct{}{} }()
	<-done
	ch.Close()
	target.Close()
}

func (s *Server) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	for req := range reqs {
		switch req.Type {
		case "exec":
			var p struct{ Command string }
			ssh.Unmarshal(req.Payload, &p)
			req.Reply(true, nil)
			go s.runExec(ctx, ch, p.Command)
		case "pty-req":
			var p struct {
				Term                   string
				Columns, Rows, Wpx, Hp uint32
				Modes                  string
			}
			ssh.Unmarshal(req.Payload, &p)
			s.mu.Lock()
			s.ptyTerm = p.Term
			s.sizes = append(s.sizes, [2]int{int(p.Columns), int(p.Rows)})
			s.mu.Unlock()
			req.Reply(true, nil)
		case "window-change":
			var p struct{ Columns, Rows, Wpx, Hpx uint32 }
			ssh.Unmarshal(req.Payload, &p)
			s.mu.Lock()
			s.sizes = append(s.sizes, [2]int{int(p.Columns), int(p.Rows)})
			s.mu.Unlock()
			req.Reply(true, nil)
		case "shell":
			req.Reply(s.opts.Shell, nil)
			if s.opts.Shell {
				go s.runShell(ch)
			}
		case "subsystem":
			var p struct{ Name string }
			ssh.Unmarshal(req.Payload, &p)
			ok := s.opts.SFTP && p.Name == "sftp"
			req.Reply(ok, nil)
			if ok {
				go func() {
					srv := sftp.NewRequestServer(ch, s.fs)
					srv.Serve()
					srv.Close()
				}()
			}
		case "signal":
			var p struct{ Signal string }
			ssh.Unmarshal(req.Payload, &p)
			s.mu.Lock()
			s.signals = append(s.signals, p.Signal)
			s.mu.Unlock()
			cancel()
		default:
			req.Reply(false, nil)
		}
	}
}

func (s *Server) runExec(ctx context.Context, ch ssh.Channel, cmd string) {
	code := 127
	if s.opts.Exec != nil {
		code = s.opts.Exec(ctx, cmd, ch, ch, ch.Stderr())
	} else {
		fmt.Fprintln(ch.Stderr(), "sshtest: no exec handler")
	}
	exit(ch, code)
}

func (s *Server) runShell(ch ssh.Channel) {
	r := bufio.NewReader(ch)
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "exit" || err != nil {
			exit(ch, 0)
			return
		}
		fmt.Fprintf(ch, "%s\r\n", line)
	}
}

// exit reports the code, then closes: closing first would lose the code.
func exit(ch ssh.Channel, code int) {
	ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
	ch.Close()
}

// Conns counts completed handshakes. It first waits, up to 2s, for
// handshakes still finishing on the server side, so a client that has just
// connected is always counted.
func (s *Server) Conns() int {
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		n, busy := s.conns, s.handshaking > 0
		s.mu.Unlock()
		if !busy || time.Now().After(deadline) {
			return n
		}
		time.Sleep(time.Millisecond)
	}
}

// Signals lists received signal names, e.g. "INT".
func (s *Server) Signals() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.signals...)
}

// PtyTerm is the term name from the last pty-req.
func (s *Server) PtyTerm() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ptyTerm
}

// WindowSizes lists [cols, rows] from pty-req, then each window-change.
func (s *Server) WindowSizes() [][2]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]int(nil), s.sizes...)
}

// DropAll closes every live connection, as a network failure would.
func (s *Server) DropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.live {
		c.Close()
	}
}

// KnownHostsLine is this server's known_hosts entry.
func (s *Server) KnownHostsLine() string {
	return s.KnownHostsLineFor(s.HostSigner.PublicKey())
}

// KnownHostsLineFor is a known_hosts entry for this address with key k.
func (s *Server) KnownHostsLineFor(k ssh.PublicKey) string {
	return knownhosts.Line([]string{knownhosts.Normalize(s.Addr)}, k)
}
