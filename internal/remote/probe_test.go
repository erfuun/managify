package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/sshtest"
	"github.com/erfuun/managify/internal/testkit"
)

const probeFixture = `0.42 0.30 0.25 1/123 4567
MemTotal:        2048000 kB
MemAvailable:    1208320 kB
Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/vda1         41152736 25600000  13456000      63% /
`

var wantStatus = Status{Load1: 0.42, RAMUsedPct: 41, DiskUsedPct: 63}

func TestParseProbe(t *testing.T) {
	got, err := ParseProbe(probeFixture)
	if err != nil || got != wantStatus {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestParseProbeLocalizedDfHeader(t *testing.T) {
	out := `1.50 0.30 0.25 1/123 4567
MemTotal:        1000 kB
MemAvailable:    250 kB
Dateisystem    1024-Blöcke   Benutzt Verfügbar Kapazität Eingehängt auf
/dev/sda1         100       81        19      81% /
/dev/sdb1         100       10        90      10% /data
`
	got, err := ParseProbe(out)
	if err != nil || got != (Status{Load1: 1.5, RAMUsedPct: 75, DiskUsedPct: 81}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestParseProbeGarbage(t *testing.T) {
	for _, out := range []string{"sh: df: not found", "", "0.42 0.30 0.25 1/123 4567\n"} {
		if _, err := ParseProbe(out); !errors.Is(err, ErrProbeOutput) {
			t.Errorf("ParseProbe(%q) err = %v", out, err)
		}
	}
}

func probeServer(t *testing.T, exit int) *sshtest.Server {
	return testkit.Start(t, sshtest.Options{Exec: func(ctx context.Context, cmd string, _ io.Reader, stdout, _ io.Writer) int {
		if cmd != ProbeCommand {
			fmt.Fprintf(stdout, "unexpected command %q", cmd)
			return 127
		}
		io.WriteString(stdout, probeFixture)
		return exit
	}})
}

func TestProbeIntegration(t *testing.T) {
	got, err := Probe(context.Background(), testkit.Dial(t, probeServer(t, 0)))
	if err != nil || got != wantStatus {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// One failing sub-command (e.g. df on an odd mount) still yields stats.
func TestProbeParsesOnNonZeroExit(t *testing.T) {
	got, err := Probe(context.Background(), testkit.Dial(t, probeServer(t, 1)))
	if err != nil || got != wantStatus {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestProbeHonorsCtxOnUnresponsiveServer(t *testing.T) {
	srv := testkit.Start(t, sshtest.Options{Exec: func(ctx context.Context, _ string, _ io.Reader, _, _ io.Writer) int {
		<-ctx.Done()
		return 1
	}})
	c := testkit.Dial(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Probe(ctx, c)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}
