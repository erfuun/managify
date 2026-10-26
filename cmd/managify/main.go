// Command managify is a terminal UI for a handful of your own Linux servers.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"managify/internal/appdir"
	"managify/internal/inventory"
	"managify/internal/secrets"
	"managify/internal/tui"
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
	deps := tui.Deps{
		Inv:     inv,
		Secrets: secrets.NewKeyring(),
		Session: secrets.NewSession(),
		DataDir: dataDir,
		Now:     time.Now,
	}
	_, err = tea.NewProgram(tui.New(deps), tea.WithAltScreen()).Run()
	return err
}
