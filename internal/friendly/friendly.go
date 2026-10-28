// Package friendly turns errors into plain words that say what to do next.
package friendly

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/remote"
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
	var jump *sshx.JumpError
	switch {
	case errors.As(err, &jump):
		name := jump.Jump.Name
		if errors.Is(jump.Err, sshx.ErrForwardingDenied) {
			return name + " doesn't allow TCP forwarding (AllowTcpForwarding in its sshd_config)."
		}
		// The fix belongs on the jump server's row, not this one.
		text := strings.Replace(Text(jump.Err, jump.Jump), "Press e", "Select "+name+" and press e", 1)
		return "Jump host " + name + ": " + text
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
		if unreach.Via != "" {
			return fmt.Sprintf("Can't reach %s via %s (%s)", unreach.Addr, unreach.Via, unreach.Reason)
		}
		return fmt.Sprintf("Can't reach %s (%s)", unreach.Addr, unreach.Reason)
	case errors.As(err, &mismatch):
		// Name the entry the way it appears in known_hosts.
		entry := knownhosts.Normalize(mismatch.Server.Addr())
		return fmt.Sprintf("Host key for %s changed! Connection blocked. If this is expected, remove the old entry for %s from %s.",
			mismatch.Server.Name, entry, mismatch.KnownHostsPath)
	case errors.Is(err, sshx.ErrHostKeyRejected):
		return "Host key not trusted. Connection cancelled."
	case errors.Is(err, sshx.ErrPromptCancelled), errors.Is(err, context.Canceled):
		return "Cancelled."
	case errors.Is(err, context.DeadlineExceeded):
		return s.Name + " did not answer in time."
	case errors.Is(err, remote.ErrSudoRejected):
		return "sudo password rejected. Press e to update it."
	case errors.Is(err, remote.ErrSudoPasswordRequired):
		return "sudo needs a password on this server. Press e and set a sudo password."
	case errors.Is(err, remote.ErrSudoNeedsTTY):
		return "This server requires a tty for sudo. Use the shell."
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
	case errors.Is(err, sshx.ErrForwardingDenied):
		return "unreachable"
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
	case errors.Is(err, sshx.ErrPromptCancelled), errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, remote.ErrSudoRejected), errors.Is(err, remote.ErrSudoPasswordRequired), errors.Is(err, remote.ErrSudoNeedsTTY):
		return "sudo failed"
	}
	return "error"
}
