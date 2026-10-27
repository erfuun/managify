package remote

import (
	"context"
	"os"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// watchResize polls the console size every 250 ms until ctx ends; Windows
// has no SIGWINCH.
func watchResize(ctx context.Context, fd int, onChange func(w, h int)) {
	lastW, lastH, _ := term.GetSize(fd)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			w, h, err := term.GetSize(fd)
			if err == nil && (w != lastW || h != lastH) {
				lastW, lastH = w, h
				onChange(w, h)
			}
		}
	}
}

// prepareOutput turns on VT processing so the console draws the remote
// side's escape sequences (colors, cursor moves, full-screen apps).
func prepareOutput(f *os.File) (restore func()) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}
	}
	windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	return func() { windows.SetConsoleMode(h, mode) }
}
