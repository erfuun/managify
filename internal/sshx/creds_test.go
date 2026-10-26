package sshx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/secrets"
)

// fakePrompter answers every prompt with Answer and counts the calls by kind.
type fakePrompter struct {
	mu     sync.Mutex
	Answer string
	Err    error
	Calls  map[PromptKind]int
	Last   PromptRequest
}

func (f *fakePrompter) Ask(_ context.Context, r PromptRequest) (PromptReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Calls == nil {
		f.Calls = map[PromptKind]int{}
	}
	f.Calls[r.Kind]++
	f.Last = r
	if f.Err != nil {
		return PromptReply{}, f.Err
	}
	return PromptReply{Text: f.Answer, Accepted: true}, nil
}

func (f *fakePrompter) count(k PromptKind) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Calls[k]
}

// brokenStore fails every call, like Linux without a Secret Service.
type brokenStore struct{}

func (brokenStore) Get(string) (string, error) { return "", errors.New("no keyring") }
func (brokenStore) Set(string, string) error   { return errors.New("no keyring") }
func (brokenStore) Delete(string) error        { return errors.New("no keyring") }

func newCreds(store secrets.Store, p Prompter) *Creds {
	return &Creds{Store: store, Session: secrets.NewSession(), Prompter: p}
}

var pwServer = inventory.Server{ID: "b81e04d7", Name: "db-1", Host: "203.0.113.20", User: "admin", Auth: inventory.AuthPassword, SavePassword: true}

func TestLoginPasswordFromKeychainDoesNotPrompt(t *testing.T) {
	store := secrets.NewMem()
	store.Set(secrets.LoginAccount(pwServer.ID), "saved")
	p := &fakePrompter{}
	pw, err := newCreds(store, p).LoginPassword(context.Background(), pwServer)
	if err != nil || pw != "saved" || p.count(PromptPassword) != 0 {
		t.Fatalf("pw=%q err=%v prompts=%d", pw, err, p.count(PromptPassword))
	}
}

func TestLoginPasswordPromptsOnceAndStores(t *testing.T) {
	store := secrets.NewMem()
	p := &fakePrompter{Answer: "typed"}
	c := newCreds(store, p)
	for i := 0; i < 2; i++ {
		pw, err := c.LoginPassword(context.Background(), pwServer)
		if err != nil || pw != "typed" {
			t.Fatalf("pw=%q err=%v", pw, err)
		}
	}
	if p.count(PromptPassword) != 1 {
		t.Fatalf("prompts = %d", p.count(PromptPassword))
	}
	if v, _ := store.Get(secrets.LoginAccount(pwServer.ID)); v != "typed" {
		t.Fatalf("keychain = %q", v)
	}
}

func TestLoginPasswordAskModeNeverTouchesKeychain(t *testing.T) {
	store := secrets.NewMem()
	store.Set(secrets.LoginAccount(pwServer.ID), "stale")
	s := pwServer
	s.SavePassword = false
	p := &fakePrompter{Answer: "typed"}
	c := newCreds(store, p)
	pw, _ := c.LoginPassword(context.Background(), s)
	if pw != "typed" {
		t.Fatalf("pw = %q", pw)
	}
	if v, _ := store.Get(secrets.LoginAccount(s.ID)); v != "stale" {
		t.Fatal("ask-mode password written to keychain")
	}
}

func TestLoginPasswordSurvivesBrokenKeychain(t *testing.T) {
	p := &fakePrompter{Answer: "typed"}
	c := newCreds(brokenStore{}, p)
	for i := 0; i < 2; i++ {
		if pw, err := c.LoginPassword(context.Background(), pwServer); err != nil || pw != "typed" {
			t.Fatalf("pw=%q err=%v", pw, err)
		}
	}
	if p.count(PromptPassword) != 1 {
		t.Fatalf("prompts = %d", p.count(PromptPassword))
	}
}

func TestLoginPasswordCancelled(t *testing.T) {
	c := newCreds(secrets.NewMem(), &fakePrompter{Err: ErrPromptCancelled})
	if _, err := c.LoginPassword(context.Background(), pwServer); !errors.Is(err, ErrPromptCancelled) {
		t.Fatalf("err = %v", err)
	}
}

func TestPassphraseCachedPerKeyPath(t *testing.T) {
	p := &fakePrompter{Answer: "phrase"}
	c := newCreds(secrets.NewMem(), p)
	for i := 0; i < 2; i++ {
		v, err := c.Passphrase(context.Background(), pwServer, `C:\keys\id`)
		if err != nil || v != "phrase" {
			t.Fatalf("v=%q err=%v", v, err)
		}
	}
	if p.count(PromptPassphrase) != 1 || p.Last.Detail != `C:\keys\id` {
		t.Fatalf("prompts=%d detail=%q", p.count(PromptPassphrase), p.Last.Detail)
	}
	c.ForgetPassphrase(`C:\keys\id`)
	c.Passphrase(context.Background(), pwServer, `C:\keys\id`)
	if p.count(PromptPassphrase) != 2 {
		t.Fatal("ForgetPassphrase did not forget")
	}
}

// Startup probes dial every server at once; a key file shared by two servers
// must be asked for once, not once per server.
func TestPassphraseConcurrentPromptsOnce(t *testing.T) {
	gate := make(chan struct{})
	p := &gatedPrompter{gate: gate, answer: "phrase"}
	c := newCreds(secrets.NewMem(), p)
	out := make(chan string, 2)
	for _, s := range []inventory.Server{{ID: "a", Name: "web-1"}, {ID: "b", Name: "web-2"}} {
		go func(s inventory.Server) {
			v, _ := c.Passphrase(context.Background(), s, `C:\keys\shared`)
			out <- v
		}(s)
	}
	time.Sleep(200 * time.Millisecond) // both callers are now waiting
	close(gate)
	for i := 0; i < 2; i++ {
		if v := <-out; v != "phrase" {
			t.Fatalf("got %q", v)
		}
	}
	if n := p.calls.Load(); n != 1 {
		t.Fatalf("prompted %d times", n)
	}
}

// gatedPrompter blocks every Ask until gate closes.
type gatedPrompter struct {
	gate   chan struct{}
	answer string
	calls  atomic.Int32
}

func (g *gatedPrompter) Ask(ctx context.Context, _ PromptRequest) (PromptReply, error) {
	g.calls.Add(1)
	<-g.gate
	return PromptReply{Text: g.answer, Accepted: true}, nil
}

func TestSudoPasswordModes(t *testing.T) {
	key := inventory.Server{ID: "7f3a9c21", Name: "web-1", Auth: inventory.AuthKey}
	cases := []struct {
		name        string
		server      inventory.Server
		keychain    map[string]string
		want        string
		wantPrompts map[PromptKind]int
		wantStored  string // keychain SudoAccount after the call
	}{
		{"password auth uses login", pwServer, map[string]string{secrets.LoginAccount(pwServer.ID): "login"}, "login", nil, ""},
		{"saved hit", with(key, inventory.SudoSaved), map[string]string{secrets.SudoAccount(key.ID): "s"}, "s", nil, "s"},
		{"saved miss prompts and stores", with(key, inventory.SudoSaved), nil, "typed", map[PromptKind]int{PromptSudo: 1}, "typed"},
		{"ask prompts, keychain untouched", with(key, inventory.SudoAsk), nil, "typed", map[PromptKind]int{PromptSudo: 1}, ""},
		{"none", with(key, inventory.SudoNone), nil, "", nil, ""},
		{"empty means none", key, nil, "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := secrets.NewMem()
			for k, v := range c.keychain {
				store.Set(k, v)
			}
			p := &fakePrompter{Answer: "typed"}
			cr := newCreds(store, p)
			got, err := cr.SudoPassword(context.Background(), c.server)
			if err != nil || got != c.want {
				t.Fatalf("got %q, %v; want %q", got, err, c.want)
			}
			for _, k := range []PromptKind{PromptPassword, PromptPassphrase, PromptSudo, PromptHostKey} {
				if p.count(k) != c.wantPrompts[k] {
					t.Fatalf("prompts[%d] = %d, want %d", k, p.count(k), c.wantPrompts[k])
				}
			}
			stored, _ := store.Get(secrets.SudoAccount(c.server.ID))
			if stored != c.wantStored {
				t.Fatalf("keychain sudo = %q, want %q", stored, c.wantStored)
			}
			// A second call never prompts again.
			cr.SudoPassword(context.Background(), c.server)
			if p.count(PromptSudo) != c.wantPrompts[PromptSudo] {
				t.Fatal("prompted twice")
			}
		})
	}
}

func TestForgetLoginClearsSessionOnly(t *testing.T) {
	store := secrets.NewMem()
	store.Set(secrets.LoginAccount(pwServer.ID), "saved")
	c := newCreds(store, &fakePrompter{})
	c.Session.Set(secrets.LoginAccount(pwServer.ID), "saved")
	c.ForgetLogin(pwServer)
	if _, ok := c.Session.Get(secrets.LoginAccount(pwServer.ID)); ok {
		t.Fatal("session not cleared")
	}
	if v, _ := store.Get(secrets.LoginAccount(pwServer.ID)); v != "saved" {
		t.Fatal("keychain was cleared")
	}
}

func TestForgetSudo(t *testing.T) {
	key := with(inventory.Server{ID: "7f3a9c21", Auth: inventory.AuthKey}, inventory.SudoAsk)
	p := &fakePrompter{Answer: "typed"}
	c := newCreds(secrets.NewMem(), p)
	c.SudoPassword(context.Background(), key)
	c.ForgetSudo(key)
	c.SudoPassword(context.Background(), key)
	if p.count(PromptSudo) != 2 {
		t.Fatalf("prompts = %d", p.count(PromptSudo))
	}
}

func with(s inventory.Server, m inventory.SudoMode) inventory.Server {
	s.SudoPassword = m
	return s
}
