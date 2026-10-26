package friendly

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/sshx"
)

var web1 = inventory.Server{ID: "7f3a9c21", Name: "web-1", Host: "203.0.113.10", Port: 22, User: "root", Auth: inventory.AuthKey}

func TestTextAndShort(t *testing.T) {
	alt := web1
	alt.Port = 2222
	const kh = `C:\Users\erfan\.ssh\known_hosts`
	cases := []struct {
		name  string
		err   error
		s     inventory.Server
		text  string
		short string
	}{
		{"password", &sshx.AuthError{Server: web1, Method: inventory.AuthPassword}, web1,
			"Password rejected. Press e to update it.", "auth failed"},
		{"key", &sshx.AuthError{Server: web1, Method: inventory.AuthKey}, web1,
			"Key rejected by server. Press e to check the key path.", "auth failed"},
		{"passphrase", &sshx.AuthError{Server: web1, Method: inventory.AuthKey, PassphraseWrong: true}, web1,
			"Wrong key passphrase.", "auth failed"},
		{"timeout", &sshx.UnreachableError{Addr: "203.0.113.10:22", Reason: "timeout"}, web1,
			"Can't reach 203.0.113.10:22 (timeout)", "timeout"},
		{"refused", &sshx.UnreachableError{Addr: "203.0.113.10:22", Reason: "connection refused"}, web1,
			"Can't reach 203.0.113.10:22 (connection refused)", "refused"},
		{"unknown host", &sshx.UnreachableError{Addr: "nope.example:22", Reason: "unknown host"}, web1,
			"Can't reach nope.example:22 (unknown host)", "unknown host"},
		{"other unreachable", &sshx.UnreachableError{Addr: "203.0.113.10:22", Reason: "EOF"}, web1,
			"Can't reach 203.0.113.10:22 (EOF)", "unreachable"},
		{"mismatch", &sshx.HostKeyMismatchError{Server: web1, KnownHostsPath: kh}, web1,
			"Host key for web-1 changed! Connection blocked. If this is expected, remove the old entry for 203.0.113.10 from " + kh + ".", "HOST KEY CHANGED"},
		{"mismatch other port", &sshx.HostKeyMismatchError{Server: alt, KnownHostsPath: kh}, alt,
			"Host key for web-1 changed! Connection blocked. If this is expected, remove the old entry for [203.0.113.10]:2222 from " + kh + ".", "HOST KEY CHANGED"},
		{"rejected", sshx.ErrHostKeyRejected, web1, "Host key not trusted. Connection cancelled.", "not trusted"},
		{"cancelled", sshx.ErrPromptCancelled, web1, "Cancelled.", "cancelled"},
		{"deadline", context.DeadlineExceeded, web1, "web-1 did not answer in time.", "timeout"},
		// Unknown errors keep their full (wrapped) message.
		{"other", errors.New("something odd"), web1, "probe: something odd", "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wrapped := fmt.Errorf("probe: %w", c.err)
			if got := Text(wrapped, c.s); got != c.text {
				t.Errorf("Text =\n %q\nwant\n %q", got, c.text)
			}
			if got := Short(wrapped); got != c.short {
				t.Errorf("Short = %q, want %q", got, c.short)
			}
		})
	}
}

func TestNilError(t *testing.T) {
	if Text(nil, web1) != "" || Short(nil) != "" {
		t.Fatal("nil error should render empty")
	}
}
