// Package friendly turns errors into plain words that say what to do next.
package friendly

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/sshx"
)

// Text is the full message for a status line or pane title.
func Text(err error, s inventory.Server) string {
	if err == nil {
		return ""
	}
	var auth *sshx.AuthError
	var unreach *sshx.UnreachableError
	var mismatch *sshx.HostKeyMismatchError
	switch {
	case errors.As(err, &auth):
		switch {
		case auth.PassphraseWrong:
			return "Wrong key passphrase."
		case auth.Method == inventory.AuthPassword:
			return "Password rejected. Press e to update it."
		default:
			return "Key rejected by server. Press e to check the key path."
		}
	case errors.As(err, &unreach):
		return fmt.Sprintf("Can't reach %s (%s)", unreach.Addr, unreach.Reason)
	case errors.As(err, &mismatch):
		// Name the entry the way it appears in known_hosts.
		entry := knownhosts.Normalize(mismatch.Server.Addr())
		return fmt.Sprintf("Host key for %s changed! Connection blocked. If this is expected, remove the old entry for %s from %s.",
			mismatch.Server.Name, entry, mismatch.KnownHostsPath)
	case errors.Is(err, sshx.ErrHostKeyRejected):
		return "Host key not trusted. Connection cancelled."
	case errors.Is(err, sshx.ErrPromptCancelled):
		return "Cancelled."
	case errors.Is(err, context.DeadlineExceeded):
		return s.Name + " did not answer in time."
	}
	return err.Error()
}

// Short is a word or two for the status column.
func Short(err error) string {
	if err == nil {
		return ""
	}
	var auth *sshx.AuthError
	var unreach *sshx.UnreachableError
	var mismatch *sshx.HostKeyMismatchError
	switch {
	case errors.As(err, &auth):
		return "auth failed"
	case errors.As(err, &unreach):
		switch unreach.Reason {
		case "timeout", "unknown host":
			return unreach.Reason
		case "connection refused":
			return "refused"
		}
		return "unreachable"
	case errors.As(err, &mismatch):
		return "HOST KEY CHANGED"
	case errors.Is(err, sshx.ErrHostKeyRejected):
		return "not trusted"
	case errors.Is(err, sshx.ErrPromptCancelled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "error"
}
