// Package outputs writes one log file per server per run under
// <data dir>/outputs/<server>/.
package outputs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/erfuun/managify/internal/appdir"
	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/remote"
)

var reservedNames = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}

func init() {
	for i := 1; i <= 9; i++ {
		reservedNames[fmt.Sprintf("COM%d", i)] = true
		reservedNames[fmt.Sprintf("LPT%d", i)] = true
	}
}

// SafeName makes a server name usable as a folder name on every OS.
func SafeName(name string) string {
	s := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	s = strings.TrimRight(s, ". ")
	if s == "" {
		return "server"
	}
	// Windows reserves these names even with an extension ("nul.txt" is NUL),
	// so the suffix goes on the part before the first dot.
	base, ext, dotted := strings.Cut(s, ".")
	if reservedNames[strings.ToUpper(base)] {
		if dotted {
			return base + "_." + ext
		}
		return s + "_"
	}
	return s
}

// Header describes the run at the top of the log.
type Header struct {
	Command  string
	Sudo     bool
	Upload   string // "local -> remote"
	Download string
}

type Log struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

// Begin creates the log and writes its header. Two runs started in the same
// second get "-2", "-3", … rather than sharing a file.
func Begin(dataDir string, s inventory.Server, h Header, start time.Time) (*Log, error) {
	dir := filepath.Join(dataDir, "outputs", SafeName(s.Name))
	if err := appdir.EnsureDir(filepath.Join(dataDir, "outputs")); err != nil {
		return nil, err
	}
	if err := appdir.EnsureDir(dir); err != nil {
		return nil, err
	}
	stamp := start.Format("20060102-150405")
	var f *os.File
	var path string
	for n := 1; ; n++ {
		path = filepath.Join(dir, stamp+".log")
		if n > 1 {
			path = filepath.Join(dir, fmt.Sprintf("%s-%d.log", stamp, n))
		}
		var err error
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	l := &Log{f: f, path: path}
	var b strings.Builder
	fmt.Fprintf(&b, "# server: %s (%s@%s)\n", s.Name, s.User, s.Addr())
	b.WriteString(comment("command: " + h.Command))
	if h.Sudo {
		b.WriteString("# sudo: yes\n")
	}
	if h.Upload != "" {
		fmt.Fprintf(&b, "# upload: %s\n", h.Upload)
	}
	if h.Download != "" {
		fmt.Fprintf(&b, "# download: %s\n", h.Download)
	}
	fmt.Fprintf(&b, "# started: %s\n", start.Format(time.RFC3339))
	if err := l.write(b.String()); err != nil {
		f.Close()
		return nil, err
	}
	return l, nil
}

func (l *Log) Path() string { return l.path }

// Line writes one output line; stderr lines are prefixed "[err] ".
func (l *Log) Line(ln remote.Line) error {
	if ln.Stream == remote.Stderr {
		return l.write("[err] " + ln.Text + "\n")
	}
	return l.write(ln.Text + "\n")
}

func (l *Log) Note(text string) error { return l.write(comment(text)) }

// comment renders text as log comment lines, so a multi-line command or
// note never reads as command output.
func comment(text string) string {
	return "# " + strings.ReplaceAll(text, "\n", "\n#   ") + "\n"
}

// Finish writes the footer and closes the file.
func (l *Log) Finish(r remote.Result) error {
	d := r.Duration.Round(100 * time.Millisecond)
	var footer string
	switch {
	case r.Cancelled:
		footer = fmt.Sprintf("# cancelled after %s (process may still be running)\n", d)
	case r.Err != nil:
		footer = "# error: " + r.Err.Error() + "\n"
	default:
		footer = fmt.Sprintf("# exit: %d  duration: %s\n", r.ExitCode, d)
	}
	err := l.write(footer)
	l.mu.Lock()
	defer l.mu.Unlock()
	return errors.Join(err, l.f.Close())
}

func (l *Log) write(s string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.f.WriteString(s)
	return err
}
