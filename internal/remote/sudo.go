package remote

import (
	"errors"
	"strings"
)

var (
	ErrSudoRejected         = errors.New("sudo password rejected")
	ErrSudoPasswordRequired = errors.New("sudo requires a password")
	ErrSudoNeedsTTY         = errors.New("sudo requires a tty")
)

// ShellQuote quotes s for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// BuildCommand wraps cmd for sudo. With a password, sudo reads it from stdin
// with an empty prompt; exec </dev/null keeps the command from reading it.
func BuildCommand(cmd string, opts RunOpts) string {
	if !opts.Sudo {
		return cmd
	}
	inner := ShellQuote("exec </dev/null; " + cmd)
	if opts.SudoPassword != "" {
		return "sudo -S -p '' -- sh -c " + inner
	}
	return "sudo -n -- sh -c " + inner
}

// sudoError recognizes sudo's own failure messages on stderr.
func sudoError(line string) error {
	switch {
	case strings.Contains(line, "incorrect password attempt"):
		return ErrSudoRejected
	case strings.Contains(line, "a password is required"):
		return ErrSudoPasswordRequired
	case strings.Contains(line, "must have a tty"), strings.Contains(line, "a terminal is required"):
		return ErrSudoNeedsTTY
	}
	return nil
}
