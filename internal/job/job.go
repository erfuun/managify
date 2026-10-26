// Package job runs one action on one server: connect, get credentials, do
// the work, log it, and report events to the UI.
package job

import (
	"context"
	"errors"
	"time"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/outputs"
	"github.com/erfuun/managify/internal/remote"
	"github.com/erfuun/managify/internal/sshx"
)

type Upload struct{ Local, Remote string }

type Spec struct {
	Command  string
	Sudo     bool
	Upload   *Upload
	Download string
}

type EventKind int

const (
	EvLine EventKind = iota
	EvNote
	EvWarn
	EvProgress
	EvUploaded
	EvDownloaded
	EvDone
)

type Event struct {
	ServerID string
	Kind     EventKind
	Line     remote.Line   // EvLine
	Text     string        // EvNote/EvWarn text, EvUploaded remote path, EvDownloaded local path
	Progress float64       // 0..1
	Result   remote.Result // EvDone
	LogPath  string        // EvDone; "" when no log was written
}

type Deps struct {
	Pool    *sshx.Pool
	Creds   *sshx.Creds
	DataDir string
	Now     func() time.Time
}

// Execute runs spec on s. EvDone is emitted exactly once, last.
func Execute(ctx context.Context, d Deps, spec Spec, s inventory.Server, emit func(Event)) {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	finish := func(r remote.Result, logPath string) {
		emit(Event{ServerID: s.ID, Kind: EvDone, Result: r, LogPath: logPath})
	}
	failed := func(err error) {
		finish(remote.Result{ExitCode: -1, Err: err, Duration: now().Sub(start)}, "")
	}
	warn := func(text string) { emit(Event{ServerID: s.ID, Kind: EvWarn, Text: text}) }

	c, err := d.Pool.Get(ctx, s)
	if err != nil {
		var auth *sshx.AuthError
		if errors.As(err, &auth) {
			d.Pool.Evict(s.ID)
		}
		failed(err)
		return
	}
	opts := remote.RunOpts{Sudo: spec.Sudo}
	if spec.Sudo {
		if opts.SudoPassword, err = d.Creds.SudoPassword(ctx, s); err != nil {
			failed(err)
			return
		}
	}

	// A log that cannot be written must not stop the command.
	log, err := outputs.Begin(d.DataDir, s, outputs.Header{Command: spec.Command, Sudo: spec.Sudo}, start)
	if err != nil {
		warn("Output is not being saved: could not create the log: " + err.Error())
		log = nil
	}
	logFailed := false
	res := remote.Run(ctx, c, spec.Command, opts, func(ln remote.Line) {
		if log != nil && !logFailed {
			if err := log.Line(ln); err != nil {
				logFailed = true
				warn("Output is no longer being saved: writing the log failed: " + err.Error())
			}
		}
		emit(Event{ServerID: s.ID, Kind: EvLine, Line: ln})
	})

	var logPath string
	if log != nil {
		logPath = log.Path()
		if err := log.Finish(res); err != nil && !logFailed {
			warn("The log may be incomplete: " + err.Error())
		}
	}
	if errors.Is(res.Err, remote.ErrSudoRejected) {
		d.Creds.ForgetSudo(s)
	}
	finish(res, logPath)
}
