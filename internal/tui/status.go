package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/friendly"
	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/remote"
	"github.com/erfuun/managify/internal/sshx"
)

const (
	defaultProbeInterval = 30 * time.Second
	probeTimeout         = 15 * time.Second
)

type probeTickMsg struct{}

// statusMsg is one probe's result. Gen ties it to the probe that started it.
type statusMsg struct {
	ID     string
	Gen    int
	Status remote.Status
	Err    error
}

type serverStatus struct {
	st  remote.Status
	err error
}

func (m Model) probeTick() tea.Cmd {
	if m.d.Pool == nil {
		return nil
	}
	every := m.d.ProbeInterval
	if every <= 0 {
		every = defaultProbeInterval
	}
	return tea.Tick(every, func(time.Time) tea.Msg { return probeTickMsg{} })
}

// probeAll is the background refresh: it skips servers still being probed
// and servers waiting for the user (no nagging).
func (m Model) probeAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range m.d.Inv.Servers {
		if m.probing[s.ID] || m.needsAction[s.ID] {
			continue
		}
		cmds = append(cmds, m.probe(s))
	}
	return tea.Batch(cmds...)
}

// probe starts a probe of s, superseding any still running.
func (m Model) probe(s inventory.Server) tea.Cmd {
	if m.d.Pool == nil {
		return nil
	}
	m.probeGen[s.ID]++
	gen := m.probeGen[s.ID]
	m.probing[s.ID] = true
	pool, ctx := m.d.Pool, m.ctx
	return func() tea.Msg {
		// No timeout on Get: the dialer has its own, paused while a prompt is open.
		c, err := pool.Get(ctx, s)
		if err != nil {
			return statusMsg{ID: s.ID, Gen: gen, Err: err}
		}
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		st, err := remote.Probe(pctx, c)
		return statusMsg{ID: s.ID, Gen: gen, Status: st, Err: err}
	}
}

func (m Model) applyStatus(msg statusMsg) Model {
	if _, ok := m.d.Inv.Get(msg.ID); !ok || msg.Gen != m.probeGen[msg.ID] {
		return m // deleted, or superseded by a newer probe
	}
	delete(m.probing, msg.ID)
	if m.ctx.Err() != nil {
		return m // quitting: errors now are ours, not the server's
	}
	m.status[msg.ID] = serverStatus{st: msg.Status, err: msg.Err}
	if needsUser(msg.Err) {
		m.needsAction[msg.ID] = true
	}
	return m
}

// needsUser reports errors only the user can fix; background probes leave
// such servers alone until the user acts on them.
func needsUser(err error) bool {
	var auth *sshx.AuthError
	var mismatch *sshx.HostKeyMismatchError
	return errors.Is(err, sshx.ErrPromptCancelled) || errors.Is(err, sshx.ErrHostKeyRejected) ||
		errors.As(err, &auth) || errors.As(err, &mismatch)
}

// retry clears s's needs-action mark and probes it now.
func (m Model) retry(s inventory.Server) tea.Cmd {
	delete(m.needsAction, s.ID)
	return m.probe(s)
}

func (m Model) forgetServer(id string) {
	if m.d.Pool != nil {
		m.d.Pool.Evict(id)
	}
	delete(m.status, id)
	delete(m.probing, id)
	delete(m.needsAction, id)
	// probeGen is kept: generations only grow, so a probe from before an edit
	// can never pass for the one started after it.
}

func (m Model) statusCell(s inventory.Server, styled bool) string {
	if m.d.Pool == nil {
		return "? unknown"
	}
	st, ok := m.status[s.ID]
	render := func(style func(...string) string, text string) string {
		if styled {
			return style(text)
		}
		return text
	}
	switch {
	case !ok:
		return "… checking"
	case st.err == nil:
		return render(upStyle.Render, fmt.Sprintf("● up   load %.1f  RAM %d%%  disk %d%%", st.st.Load1, st.st.RAMUsedPct, st.st.DiskUsedPct))
	case errors.Is(st.err, remote.ErrProbeOutput):
		return render(upStyle.Render, "● up   (no stats)")
	}
	return render(downStyle.Render, "○ down ("+friendly.Short(st.err)+")")
}

// downText explains why s is down, or "" if it is not.
func (m Model) downText(s inventory.Server) string {
	st, ok := m.status[s.ID]
	if !ok || st.err == nil || errors.Is(st.err, remote.ErrProbeOutput) {
		return ""
	}
	return friendly.Text(st.err, s)
}
