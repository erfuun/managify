//go:build !windows

package remote

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// watchResize reports the terminal size on each SIGWINCH until ctx ends.
func watchResize(ctx context.Context, fd int, onChange func(w, h int)) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGWINCH)
	defer signal.Stop(sig)
	for {
		select {
		case <-ctx.Done():
			return
		case <-sig:
			if w, h, err := term.GetSize(fd); err == nil {
				onChange(w, h)
			}
		}
	}
}

// prepareOutput has nothing to do on Unix terminals.
func prepareOutput(*os.File) (restore func()) { return func() {} }
