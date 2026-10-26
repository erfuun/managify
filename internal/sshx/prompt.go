// Package sshx dials servers: credentials, host-key trust and a connection pool.
package sshx

import (
	"context"
	"errors"

	"managify/internal/inventory"
)

type PromptKind int

const (
	PromptPassword PromptKind = iota
	PromptPassphrase
	PromptSudo
	PromptHostKey
)

type PromptRequest struct {
	Server inventory.Server
	Kind   PromptKind
	Detail string // key path (passphrase) or "SHA256:…" fingerprint (host key)
}

// PromptReply.Accepted is false when the user cancelled or declined.
type PromptReply struct {
	Text     string
	Accepted bool
}

var ErrPromptCancelled = errors.New("prompt cancelled")

type Prompter interface {
	Ask(ctx context.Context, r PromptRequest) (PromptReply, error)
}

// Pending is one prompt waiting for the UI to answer it.
type Pending struct {
	Req   PromptRequest
	reply chan PromptReply
	done  <-chan struct{}
}

// Reply answers the prompt. It never blocks, even if the asker gave up.
func (p *Pending) Reply(r PromptReply) {
	select {
	case p.reply <- r:
	default:
	}
}

// Done closes when the asker stopped waiting, so the UI can drop a stale prompt.
func (p *Pending) Done() <-chan struct{} { return p.done }

// Queue hands prompts to the UI one at a time: when several servers need a
// password at once, the second asker waits until the first is answered.
type Queue struct {
	C   <-chan *Pending
	c   chan *Pending
	sem chan struct{}
}

func NewQueue() *Queue {
	c := make(chan *Pending)
	return &Queue{C: c, c: c, sem: make(chan struct{}, 1)}
}

func (q *Queue) Ask(ctx context.Context, r PromptRequest) (PromptReply, error) {
	if ctx.Err() != nil { // select picks randomly among ready cases
		return PromptReply{}, ErrPromptCancelled
	}
	select {
	case q.sem <- struct{}{}:
	case <-ctx.Done():
		return PromptReply{}, ErrPromptCancelled
	}
	defer func() { <-q.sem }()

	p := &Pending{Req: r, reply: make(chan PromptReply, 1), done: ctx.Done()}
	select {
	case q.c <- p:
	case <-ctx.Done():
		return PromptReply{}, ErrPromptCancelled
	}
	select {
	case rep := <-p.reply:
		if !rep.Accepted {
			return rep, ErrPromptCancelled
		}
		return rep, nil
	case <-ctx.Done():
		return PromptReply{}, ErrPromptCancelled
	}
}
