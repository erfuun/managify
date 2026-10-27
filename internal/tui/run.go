package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/erfuun/managify/internal/friendly"
	"github.com/erfuun/managify/internal/inventory"
	"github.com/erfuun/managify/internal/job"
	"github.com/erfuun/managify/internal/remote"
	"github.com/erfuun/managify/internal/sshx"
)

const (
	maxPaneLines = 5000 // the log keeps everything; a pane only the tail
	maxStacked   = 3    // more targets than this get tabs
	batchWindow  = 50 * time.Millisecond
)

// runBatchMsg carries the events that arrived within one batch window.
// closed means every job has finished and the channel is drained.
type runBatchMsg struct {
	run    *runModel
	events []job.Event
	closed bool
}

type runModel struct {
	spec    job.Spec
	cancel  context.CancelFunc
	events  chan job.Event
	panes   []*pane
	byID    map[string]*pane
	focus   int
	running int
	hint    string
}

type pane struct {
	server inventory.Server
	lines  []string // rendered; trimmed to the last maxPaneLines
	vp     viewport.Model
	done   bool
	res    remote.Result

	// The transfer's progress line, updated in place; prog is -1 when none.
	prog      int
	progLabel string
}

// startRun runs spec on every target at once and opens the run view.
func (m Model) startRun(spec job.Spec, targets []inventory.Server) (Model, tea.Cmd) {
	if m.d.Pool == nil || len(targets) == 0 {
		m.screen = screenList
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	r := &runModel{spec: spec, cancel: cancel, events: make(chan job.Event, 256),
		byID: map[string]*pane{}, running: len(targets)}
	jd := job.Deps{Pool: m.d.Pool, Creds: m.d.Creds, DataDir: m.d.DataDir, Now: m.d.Now}
	app := m.ctx
	var wg sync.WaitGroup
	for _, s := range targets {
		p := &pane{server: s, vp: viewport.New(0, 0), prog: -1}
		r.panes = append(r.panes, p)
		r.byID[s.ID] = p
		wg.Add(1)
		go func() {
			defer wg.Done()
			job.Execute(ctx, jd, spec, s, func(ev job.Event) {
				// Blocking here slows the server's reader, which is fine; only
				// quitting the app drops events.
				select {
				case r.events <- ev:
				case <-app.Done():
				}
			})
		}()
	}
	go func() {
		wg.Wait()
		close(r.events)
	}()
	m.run = r
	m.screen = screenRun
	m.layoutRun()
	return m, listen(r)
}

// listen waits for the next event, then collects whatever else arrives
// within batchWindow, so a flood of lines costs one update per batch.
func listen(r *runModel) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-r.events
		if !ok {
			return runBatchMsg{run: r, closed: true}
		}
		evs := []job.Event{ev}
		timeout := time.After(batchWindow)
		for {
			select {
			case ev, ok := <-r.events:
				if !ok {
					return runBatchMsg{run: r, events: evs, closed: true}
				}
				evs = append(evs, ev)
			case <-timeout:
				return runBatchMsg{run: r, events: evs}
			}
		}
	}
}

func (m Model) applyRunBatch(msg runBatchMsg) (Model, tea.Cmd) {
	r := msg.run
	touched := map[*pane]bool{}
	var probes []tea.Cmd
	for _, ev := range msg.events {
		p := r.byID[ev.ServerID]
		if p == nil {
			continue
		}
		switch ev.Kind {
		case job.EvLine:
			text := cleanText(ev.Line.Text)
			if ev.Line.Stream == remote.Stderr {
				text = stderrStyle.Render(text)
			}
			p.add(text)
		case job.EvNote:
			p.add(hintStyle.Render("# " + cleanText(ev.Text)))
		case job.EvWarn:
			p.add(stderrStyle.Render("! " + cleanText(ev.Text)))
		case job.EvProgress:
			p.progress(ev.Text, ev.Progress)
		case job.EvUploaded:
			m.rememberRemoteDir(ev.ServerID, ev.Text)
		case job.EvDone:
			p.done, p.res = true, ev.Result
			r.running--
			if r.running == 0 {
				r.hint = ""
			}
			// The run is the explicit retry: its outcome decides whether
			// background probes may ask about this server again.
			// A cancelled prompt may be sudo's, after a good connect, so it
			// leaves the mark as it was; the other errors come only from connecting.
			if _, ok := m.d.Inv.Get(ev.ServerID); ok && m.ctx.Err() == nil {
				switch {
				case errors.Is(ev.Result.Err, sshx.ErrPromptCancelled):
				case needsUser(ev.Result.Err):
					m.needsAction[ev.ServerID] = true
				default:
					delete(m.needsAction, ev.ServerID)
					if !m.probing[ev.ServerID] {
						probes = append(probes, m.probe(p.server))
					}
				}
			}
		}
		touched[p] = true
	}
	for p := range touched {
		p.refresh()
	}
	if !msg.closed && r == m.run {
		probes = append(probes, listen(r))
	}
	return m, tea.Batch(probes...)
}

func (p *pane) add(line string) {
	p.lines = append(p.lines, line)
	if len(p.lines) > maxPaneLines+1000 { // trim in steps, not on every line
		cut := len(p.lines) - maxPaneLines
		p.lines = append([]string(nil), p.lines[cut:]...)
		if p.prog -= cut; p.prog < 0 {
			p.prog = -1
		}
	}
}

// progress shows "label N%", replacing the previous line for the same label.
func (p *pane) progress(label string, f float64) {
	text := fmt.Sprintf("%s %d%%", label, int(f*100))
	if p.prog >= 0 && p.progLabel == label {
		p.lines[p.prog] = text
		return
	}
	p.add(text)
	p.prog, p.progLabel = len(p.lines)-1, label
}

// refresh shows the latest lines, following the output only when the pane
// was already scrolled to the bottom.
func (p *pane) refresh() {
	follow := p.vp.AtBottom()
	lines := p.lines[max(len(p.lines)-maxPaneLines, 0):]
	p.vp.SetContent(strings.Join(lines, "\n"))
	if follow {
		p.vp.GotoBottom()
	}
}

func (r *runModel) tabbed() bool { return len(r.panes) > maxStacked }

// layoutRun sizes the panes to the window: stacked panes share the height,
// tabs show one pane at a time.
func (m *Model) layoutRun() {
	r := m.run
	if r == nil {
		return
	}
	h := m.height
	if h <= 0 {
		h = 24
	}
	avail := h - 4 // header, footer and hint lines
	if m.prompt != nil {
		avail -= strings.Count(m.viewPrompt(), "\n")
	}
	shown := len(r.panes)
	if r.tabbed() {
		avail--
		shown = 1
	}
	each := max(avail/shown-1, 1) // each pane has a title line
	for _, p := range r.panes {
		p.vp.Width, p.vp.Height = m.viewWidth()-2, each
		p.refresh()
	}
}

func (m Model) updateRun(msg tea.Msg) (tea.Model, tea.Cmd) {
	r := m.run
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c":
		r.cancel()
	case "ctrl+s":
		return m.saveRunAsSnippet()
	case "esc":
		if r.running > 0 {
			r.hint = "ctrl+c to cancel"
			return m, nil
		}
		r.cancel()
		m.screen = screenList
	case "tab":
		r.focus = (r.focus + 1) % len(r.panes)
	case "shift+tab":
		r.focus = (r.focus + len(r.panes) - 1) % len(r.panes)
	case "pgup":
		r.panes[r.focus].vp.PageUp()
	case "pgdown":
		r.panes[r.focus].vp.PageDown()
	}
	return m, nil
}

func (p *pane) title() string {
	name := p.server.Name
	switch {
	case !p.done:
		return name + " · running…"
	case p.res.Cancelled:
		return fmt.Sprintf("%s ✗ cancelled locally; process may still be running on %s", name, name)
	case p.res.Err != nil:
		return name + " ✗ " + friendly.Text(p.res.Err, p.server)
	case p.res.ExitCode != 0:
		return fmt.Sprintf("%s ✗ %d", name, p.res.ExitCode)
	}
	return name + " ✓ 0"
}

func (m Model) viewRun() string {
	r := m.run
	var b strings.Builder
	head := "Run: " + r.spec.Command
	switch {
	case r.spec.Download != "":
		head = "Download: " + r.spec.Download
	case r.spec.Upload != nil:
		head = "Upload: " + filepath.Base(r.spec.Upload.Local) + " → " + r.spec.Upload.Remote
		if r.spec.Command != "" {
			head += " · then: " + r.spec.Command
		}
	}
	if r.spec.Sudo {
		head += "  (sudo)"
	}
	b.WriteString(" " + titleStyle.Render(head) + "\n")
	shown := r.panes
	if r.tabbed() {
		tabs := make([]string, len(r.panes))
		for i, p := range r.panes {
			if i == r.focus {
				tabs[i] = "[" + p.server.Name + "]"
			} else {
				tabs[i] = " " + p.server.Name + " "
			}
		}
		b.WriteString(" " + strings.Join(tabs, " ") + "\n")
		shown = r.panes[r.focus : r.focus+1]
	}
	for _, p := range shown {
		style := hintStyle
		switch {
		case p.done && p.res.ExitCode == 0 && p.res.Err == nil && !p.res.Cancelled:
			style = upStyle
		case p.done:
			style = downStyle
		}
		marker := "  "
		if !r.tabbed() && len(r.panes) > 1 && p == r.panes[r.focus] {
			marker = "› "
		}
		b.WriteString(marker + style.Render(p.title()) + "\n")
		for _, line := range strings.Split(p.vp.View(), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	if r.hint != "" {
		b.WriteString(" " + errorStyle.Render(r.hint) + "\n")
	}
	if r.spec.Download != "" {
		b.WriteString(" " + hintStyle.Render("files: "+downloadsHint(m.d.DataDir)) + "\n")
	}
	b.WriteString(" " + hintStyle.Render("logs: "+filepath.Join(m.d.DataDir, "outputs")+string(filepath.Separator)) + "\n")
	keys := "pgup/pgdn scroll · ctrl+c cancel · esc back"
	if r.running == 0 && r.spec.Download == "" && m.d.Snippets != nil {
		keys = "pgup/pgdn scroll · ctrl+s save as snippet · esc back"
	}
	if len(r.panes) > 1 {
		keys = "tab next pane · " + keys
	}
	b.WriteString(" " + hintStyle.Render(keys) + "\n")
	return b.String()
}
