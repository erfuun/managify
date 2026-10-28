// Package job runs one action on one server: connect, get credentials, do
// the work, log it, and report events to the UI.
package job

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
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
	// JumpName names the server's jump server for the log header; nil or ""
	// leaves "via" out.
	JumpName func(inventory.Server) string
}

// Execute runs spec on s: an optional upload, then an optional command, or a
// download. EvDone is emitted exactly once, last.
func Execute(ctx context.Context, d Deps, spec Spec, s inventory.Server, emit func(Event)) {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	x := &execution{d: d, s: s, emit: emit, start: now(), now: now}

	c, err := d.Pool.Get(ctx, s)
	if err != nil {
		var auth *sshx.AuthError
		if errors.As(err, &auth) {
			d.Pool.Evict(s.ID)
		}
		x.failed(err)
		return
	}
	opts := remote.RunOpts{Sudo: spec.Sudo && spec.Command != ""}
	if opts.Sudo {
		if opts.SudoPassword, err = d.Creds.SudoPassword(ctx, s); err != nil {
			x.failed(err)
			return
		}
	}

	x.begin(header(spec))
	var res remote.Result
	switch {
	case spec.Download != "":
		res = x.download(ctx, c, spec.Download)
	case spec.Upload != nil:
		if res = x.upload(ctx, c, *spec.Upload); res.Err == nil && spec.Command != "" {
			res = x.run(ctx, c, spec.Command, opts)
		}
	default:
		res = x.run(ctx, c, spec.Command, opts)
	}
	logPath := x.finishLog(res)
	if errors.Is(res.Err, remote.ErrSudoRejected) {
		d.Creds.ForgetSudo(s)
	}
	emit(Event{ServerID: s.ID, Kind: EvDone, Result: res, LogPath: logPath})
}

// header is the log header; a transfer without a command says so.
func header(spec Spec) outputs.Header {
	h := outputs.Header{Command: spec.Command, Sudo: spec.Sudo && spec.Command != "", Download: spec.Download}
	if spec.Upload != nil {
		h.Upload = spec.Upload.Local + " -> " + spec.Upload.Remote
	}
	if h.Command == "" {
		h.Command = "(none)"
	}
	return h
}

// execution is one Execute call's state: the server, the log, the clock.
type execution struct {
	d         Deps
	s         inventory.Server
	emit      func(Event)
	start     time.Time
	now       func() time.Time
	log       *outputs.Log
	logFailed bool
}

func (x *execution) failed(err error) {
	x.emit(Event{ServerID: x.s.ID, Kind: EvDone, Result: x.result(err)})
}

func (x *execution) result(err error) remote.Result {
	return remote.Result{ExitCode: -1, Err: err, Duration: x.now().Sub(x.start)}
}

func (x *execution) warn(text string) { x.emit(Event{ServerID: x.s.ID, Kind: EvWarn, Text: text}) }

// begin opens the log. One that cannot be written must not stop the job.
func (x *execution) begin(h outputs.Header) {
	if x.d.JumpName != nil {
		h.Via = x.d.JumpName(x.s)
	}
	log, err := outputs.Begin(x.d.DataDir, x.s, h, x.start)
	if err != nil {
		x.warn("Output is not being saved: could not create the log: " + err.Error())
		return
	}
	x.log = log
}

func (x *execution) logWrite(write func(*outputs.Log) error) {
	if x.log == nil || x.logFailed {
		return
	}
	if err := write(x.log); err != nil {
		x.logFailed = true
		x.warn("Output is no longer being saved: writing the log failed: " + err.Error())
	}
}

// note goes to both the pane and the log.
func (x *execution) note(kind EventKind, text string) {
	x.emit(Event{ServerID: x.s.ID, Kind: kind, Text: text})
	x.logWrite(func(l *outputs.Log) error { return l.Note(text) })
}

func (x *execution) finishLog(res remote.Result) string {
	if x.log == nil {
		return ""
	}
	if err := x.log.Finish(res); err != nil && !x.logFailed {
		x.warn("The log may be incomplete: " + err.Error())
	}
	return x.log.Path()
}

func (x *execution) run(ctx context.Context, c sshx.Client, cmd string, opts remote.RunOpts) remote.Result {
	return remote.Run(ctx, c, cmd, opts, func(ln remote.Line) {
		x.logWrite(func(l *outputs.Log) error { return l.Line(ln) })
		x.emit(Event{ServerID: x.s.ID, Kind: EvLine, Line: ln})
	})
}

func (x *execution) progress(label string) remote.Progress {
	return func(done, total int64) {
		f := 1.0
		if total > 0 {
			f = float64(done) / float64(total)
		}
		x.emit(Event{ServerID: x.s.ID, Kind: EvProgress, Text: label, Progress: f})
	}
}

func (x *execution) upload(ctx context.Context, c sshx.Client, u Upload) remote.Result {
	name := filepath.Base(u.Local)
	if crlf, err := remote.HasCRLF(u.Local); err == nil && crlf {
		x.note(EvWarn, fmt.Sprintf(`warning: %s has Windows line endings (CRLF); shell scripts may fail with $'\r': command not found`, name))
	}
	final, err := remote.Upload(ctx, c, u.Local, u.Remote, x.progress("uploading "+name))
	if err != nil {
		return x.result(err)
	}
	x.emit(Event{ServerID: x.s.ID, Kind: EvUploaded, Text: final})
	x.note(EvNote, "uploaded to "+final)
	return remote.Result{Duration: x.now().Sub(x.start)}
}

func (x *execution) download(ctx context.Context, c sshx.Client, remotePath string) remote.Result {
	dir := filepath.Join(x.d.DataDir, "downloads", outputs.SafeName(x.s.Name))
	local, err := remote.Download(ctx, c, remotePath, dir, x.progress("downloading "+path.Base(remotePath)))
	if err != nil {
		return x.result(err)
	}
	x.emit(Event{ServerID: x.s.ID, Kind: EvDownloaded, Text: local})
	x.note(EvNote, "downloaded to "+local)
	return remote.Result{Duration: x.now().Sub(x.start)}
}
