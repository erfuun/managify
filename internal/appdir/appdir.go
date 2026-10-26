// Package appdir knows where Managify keeps its files.
package appdir

import (
	"os"
	"path/filepath"
	"strings"
)

// ConfigDir is os.UserConfigDir()/managify (servers.yaml, snippets.yaml).
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "managify"), nil
}

// DataDir is <home>/Managify (outputs/ and downloads/), kept somewhere easy to find.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Managify"), nil
}

// KnownHostsPath is <home>/.ssh/known_hosts, shared with OpenSSH.
func KnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// ExpandHome turns "~", "~/x" and `~\x` into paths under the home directory.
// Anything else is returned unchanged.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	rest := strings.ReplaceAll(p[1:], `\`, "/")
	return filepath.Join(home, filepath.FromSlash(rest))
}

// EnsureDir creates path and its parents with user-only permissions.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0700)
}
