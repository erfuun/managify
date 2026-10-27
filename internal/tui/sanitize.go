package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const tabWidth = 8

// cleanText makes one line of server output safe to draw in a pane. The
// terminal would otherwise run its escape sequences, expand its tabs past the
// pane's width and let "\r" overwrite the gutter. Logs keep the raw text.
func cleanText(s string) string {
	s = strings.TrimSuffix(s, "\r")
	s = ansi.Strip(s)
	// A progress meter redraws with "\r"; only the last state is visible.
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			// C0 and C1 controls draw nothing useful and some steer the terminal.
		default:
			b.WriteRune(r)
			col += max(ansi.StringWidth(string(r)), 0)
		}
	}
	return b.String()
}
