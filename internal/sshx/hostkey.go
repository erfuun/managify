package sshx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"managify/internal/inventory"
)

// HostKeyMismatchError means the server presented a different key than the
// one remembered. The connection is blocked; there is no override in the app.
type HostKeyMismatchError struct {
	Server         inventory.Server
	KnownHostsPath string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("host key for %s changed (see %s)", e.Server.Name, e.KnownHostsPath)
}

var ErrHostKeyRejected = errors.New("host key not trusted")

// knownHostsMu serializes appends: several first-time hosts can be confirmed
// back to back by concurrent dials.
var knownHostsMu sync.Mutex

// HostKeyCallback verifies against knownHostsPath (created if missing). An
// unknown host is trusted only after the user confirms its fingerprint.
func HostKeyCallback(ctx context.Context, knownHostsPath string, s inventory.Server, p Prompter) (ssh.HostKeyCallback, error) {
	if err := ensureFile(knownHostsPath); err != nil {
		return nil, err
	}
	if _, err := knownhosts.New(knownHostsPath); err != nil {
		return nil, fmt.Errorf("reading %s: %w", knownHostsPath, err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// Re-read: another dial may have just added a host.
		check, err := knownhosts.New(knownHostsPath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", knownHostsPath, err)
		}
		err = check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		var re *knownhosts.RevokedError
		switch {
		case errors.As(err, &ke) && len(ke.Want) == 0:
			_, perr := p.Ask(ctx, PromptRequest{Server: s, Kind: PromptHostKey, Detail: ssh.FingerprintSHA256(key)})
			if errors.Is(perr, ErrPromptCancelled) {
				return ErrHostKeyRejected
			}
			if perr != nil {
				return perr
			}
			return appendKnownHost(knownHostsPath, hostname, key)
		case errors.As(err, &ke), errors.As(err, &re):
			return &HostKeyMismatchError{Server: s, KnownHostsPath: knownHostsPath}
		}
		return err
	}, nil
}

func ensureFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}

// appendKnownHost adds one line, first ending the user's last line if it
// lacks a newline so both stay valid.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err != nil && err != io.EOF {
			return err
		}
		if last[0] != '\n' {
			line = "\n" + line
		}
	}
	_, err = f.WriteString(line)
	return err
}
