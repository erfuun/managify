// Package remote does the work on a server: status probes, commands, files.
package remote

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/erfuun/managify/internal/sshx"
)

const ProbeCommand = "cat /proc/loadavg; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo; df -P /"

// ErrProbeOutput means the server answered but its output had no stats:
// the server is up, the numbers are just unavailable.
var ErrProbeOutput = errors.New("probe: unexpected output")

type Status struct {
	Load1       float64
	RAMUsedPct  int
	DiskUsedPct int
}

// ParseProbe reads ProbeCommand's output. df is read by position, not by its
// header, because headers are translated under other locales.
func ParseProbe(out string) (Status, error) {
	var st Status
	var haveLoad, haveDisk bool
	var memTotal, memAvail float64 = -1, -1
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case len(f) >= 2 && (f[0] == "MemTotal:" || f[0] == "MemAvailable:"):
			v, err := strconv.ParseFloat(f[1], 64)
			if err != nil {
				continue
			}
			if f[0] == "MemTotal:" {
				memTotal = v
			} else {
				memAvail = v
			}
		case !haveLoad && len(f) == 5 && strings.Contains(f[3], "/"):
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				st.Load1, haveLoad = v, true
			}
		case len(f) >= 6 && f[len(f)-1] == "/" && strings.HasSuffix(f[4], "%"):
			if v, err := strconv.Atoi(strings.TrimSuffix(f[4], "%")); err == nil {
				st.DiskUsedPct, haveDisk = v, true
			}
		}
	}
	if !haveLoad || !haveDisk || memTotal <= 0 || memAvail < 0 {
		return Status{}, fmt.Errorf("%w: %.80q", ErrProbeOutput, out)
	}
	st.RAMUsedPct = int(math.Round((memTotal - memAvail) * 100 / memTotal))
	return st, nil
}

func Probe(ctx context.Context, c sshx.Client) (Status, error) {
	sess, err := openSession(ctx, c)
	if err != nil {
		return Status{}, err
	}
	defer sess.Close()
	stop := context.AfterFunc(ctx, func() { sess.Close() })
	defer stop()
	out, err := sess.Output(ProbeCommand)
	if ctx.Err() != nil {
		return Status{}, ctx.Err()
	}
	st, perr := ParseProbe(string(out))
	if perr == nil {
		return st, nil
	}
	var exit *ssh.ExitError
	if err != nil && !errors.As(err, &exit) {
		return Status{}, err
	}
	return Status{}, perr
}

// openSession is NewSession bounded by ctx: on a half-open connection
// NewSession blocks until keepalive gives up on it.
func openSession(ctx context.Context, c sshx.Client) (*ssh.Session, error) {
	type result struct {
		s   *ssh.Session
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := c.NewSession()
		ch <- result{s, err}
	}()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.s != nil {
				r.s.Close()
			}
		}()
		return nil, ctx.Err()
	}
}
