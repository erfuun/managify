package sshx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	"managify/internal/inventory"
)

// AuthError means the server refused the credentials.
type AuthError struct {
	Server          inventory.Server
	Method          inventory.Auth
	PassphraseWrong bool
}

func (e *AuthError) Error() string {
	if e.PassphraseWrong {
		return fmt.Sprintf("wrong key passphrase for %s", e.Server.Name)
	}
	return fmt.Sprintf("%s authentication failed for %s", e.Method, e.Server.Name)
}

// UnreachableError means no SSH conversation happened.
type UnreachableError struct {
	Addr   string
	Reason string // "timeout", "connection refused", or other text
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("can't reach %s (%s)", e.Addr, e.Reason)
}

// netError classifies a failed TCP dial or a dead handshake.
func netError(ctx context.Context, addr string, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ErrPromptCancelled
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &UnreachableError{Addr: addr, Reason: "timeout"}
	}
	if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "refused") {
		return &UnreachableError{Addr: addr, Reason: "connection refused"}
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return &UnreachableError{Addr: addr, Reason: "unknown host"}
	}
	return &UnreachableError{Addr: addr, Reason: err.Error()}
}
