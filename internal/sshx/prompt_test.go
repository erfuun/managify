package sshx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/inventory"
)

func recv(t *testing.T, q *Queue) *Pending {
	t.Helper()
	select {
	case p := <-q.C:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("no prompt delivered")
		return nil
	}
}

func noneWaiting(t *testing.T, q *Queue) {
	t.Helper()
	select {
	case p := <-q.C:
		t.Fatalf("unexpected second prompt for %s", p.Req.Server.Name)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestQueueSerializes(t *testing.T) {
	q := NewQueue()
	type res struct {
		name string
		rep  PromptReply
		err  error
	}
	out := make(chan res, 2)
	for _, n := range []string{"web-1", "db-1"} {
		go func(n string) {
			rep, err := q.Ask(context.Background(), PromptRequest{Server: inventory.Server{Name: n}, Kind: PromptPassword})
			out <- res{n, rep, err}
		}(n)
	}
	first := recv(t, q)
	noneWaiting(t, q) // the second asker is blocked until the first is answered
	first.Reply(PromptReply{Text: "pw-" + first.Req.Server.Name, Accepted: true})
	second := recv(t, q)
	second.Reply(PromptReply{Text: "pw-" + second.Req.Server.Name, Accepted: true})
	for i := 0; i < 2; i++ {
		r := <-out
		if r.err != nil || r.rep.Text != "pw-"+r.name {
			t.Fatalf("%s got %+v %v", r.name, r.rep, r.err)
		}
	}
}

func TestQueueDeclinedIsCancelled(t *testing.T) {
	q := NewQueue()
	go func() { recv(t, q).Reply(PromptReply{Accepted: false}) }()
	if _, err := q.Ask(context.Background(), PromptRequest{}); !errors.Is(err, ErrPromptCancelled) {
		t.Fatalf("err = %v", err)
	}
}

func TestQueueCancelledWhileWaitingForLock(t *testing.T) {
	q := NewQueue()
	go q.Ask(context.Background(), PromptRequest{}) // holds the queue
	held := recv(t, q)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := q.Ask(ctx, PromptRequest{}); !errors.Is(err, ErrPromptCancelled) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancel did not unblock the waiting asker")
	}
	held.Reply(PromptReply{Accepted: true})
}

func TestQueueCancelledAfterDelivery(t *testing.T) {
	q := NewQueue()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := q.Ask(ctx, PromptRequest{})
		errc <- err
	}()
	p := recv(t, q)
	cancel()
	if err := <-errc; !errors.Is(err, ErrPromptCancelled) {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-p.Done():
	default:
		t.Fatal("Done not closed after requester cancelled")
	}
	// A late reply must not block the UI, and the queue is free again.
	replied := make(chan struct{})
	go func() { p.Reply(PromptReply{Accepted: true}); close(replied) }()
	select {
	case <-replied:
	case <-time.After(time.Second):
		t.Fatal("late Reply blocked")
	}
	go func() { recv(t, q).Reply(PromptReply{Text: "x", Accepted: true}) }()
	if rep, err := q.Ask(context.Background(), PromptRequest{}); err != nil || rep.Text != "x" {
		t.Fatalf("next ask: %+v %v", rep, err)
	}
}
