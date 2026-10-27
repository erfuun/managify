// Command managify is a terminal UI for a handful of your own Linux servers.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/appdir"
	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
	"github.com/erfuun/managify/internal/snippets"
	"github.com/erfuun/managify/internal/sshx"
	"github.com/erfuun/managify/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "managify:", err)
		os.Exit(1)
	}
}

func run() error {
	configDir, err := appdir.ConfigDir()
	if err != nil {
		return err
	}
	dataDir, err := appdir.DataDir()
	if err != nil {
		return err
	}
	for _, d := range []string{configDir, dataDir} {
		if err := appdir.EnsureDir(d); err != nil {
			return err
		}
	}
	// A file that does not parse is reported and left alone, never overwritten.
	inv, err := inventory.Load(filepath.Join(configDir, "servers.yaml"))
	if err != nil {
		return err
	}
	snips, err := snippets.Load(filepath.Join(configDir, "snippets.yaml"))
	if err != nil {
		return err
	}
	knownHosts, err := appdir.KnownHostsPath()
	if err != nil {
		return err
	}
	store, session := secrets.NewKeyring(), secrets.NewSession()
	prompts := sshx.NewQueue()
	creds := &sshx.Creds{Store: store, Session: session, Prompter: prompts}
	pool := sshx.NewPool(&sshx.Dialer{KnownHosts: knownHosts, Creds: creds})
	defer pool.Close()

	deps := tui.Deps{
		Inv:      inv,
		Secrets:  store,
		Session:  session,
		DataDir:  dataDir,
		Now:      time.Now,
		Snippets: snips,
		Pool:     pool,
		Creds:    creds,
		Prompts:  prompts,
	}
	_, err = tea.NewProgram(tui.New(deps), tea.WithAltScreen()).Run()
	return err
}
