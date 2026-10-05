package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/core/transcript"
	"github.com/sebastianrcnt/atto/goal"
	"github.com/sebastianrcnt/atto/images"
	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/server"
	"github.com/sebastianrcnt/atto/tui"
)

// /remote serves this TUI's own session to a phone or browser: the same
// protocol and web client as atto serve (package server), with the
// session as the server's only thread (server.Live). The browser sees the
// transcript, follows it live, and sends messages as if typed here; all
// of it shows in the terminal too. Each start makes a new token, so
// /remote off revokes the link.
//
// HTTP handlers reach the App through remoteSession, whose methods run under
// the UI lock (a.ui.Do) like everything else that touches the App; the
// App publishes notifications from there too, in order.

// defaultRemotePort is /remote's port unless settings.json's
// "remote": {"port": N} or /remote on <port> says otherwise.
const defaultRemotePort = 7879

// remote is a running /remote server.
type remote struct {
	srv     *http.Server
	api     *server.Server
	addr    string // what it listens on
	token   string
	links   []string
	clients int
	// thread is the session the clients were last told about.
	thread string
	// turnSeq numbers the runs; turnID is the running one's.
	turnSeq int
	turnID  string
	// last is the thread state last published (thread/updated).
	last server.ThreadInfo
	// notices numbers the notices sent as items; notes are those of the
	// session shown, with the number of transcript items before each, so
	// thread/read puts them back in place.
	notices int
	notes   []remoteNote
	// goal is the goal last published (goal/updated).
	goal *server.GoalInfo
}

type remoteNote struct {
	at   int
	item server.Item
}

// maxRemoteNotes bounds the notices kept for thread/read.
const maxRemoteNotes = 200

var errRemoteOff = errors.New("remote control is off")

func (a *App) cmdRemote(arg string) {
	fields := strings.Fields(arg)
	verb := ""
	if len(fields) > 0 {
		verb = fields[0]
	}
	switch verb {
	case "":
		if a.remote != nil {
			a.showRemote(a.remote)
			return
		}
		a.startRemote(0)
	case "on":
		port := 0
		if len(fields) > 1 {
			p, err := strconv.Atoi(fields[1])
			if err != nil || p < 1 || p > 65535 {
				a.notice("Not a port: %s. Usage: /remote on [port]", fields[1])
				return
			}
			port = p
		}
		if r := a.remote; r != nil {
			if _, cur, _ := net.SplitHostPort(r.addr); port == 0 || strconv.Itoa(port) == cur {
				a.showRemote(r)
				return
			}
			a.stopRemote()
		}
		a.startRemote(port)
	case "off":
		if a.remote == nil {
			a.notice("Remote control is off.")
			return
		}
		a.stopRemote()
		a.notice("Remote control stopped. Its link no longer works.")
	default:
		a.notice("Usage: /remote [on [port]|off]")
	}
}

// startRemote listens on port (0: the configured one) on every interface.
func (a *App) startRemote(port int) {
	if port == 0 {
		port = defaultRemotePort
		if s, err := config.LoadSettings(); err == nil && s.Remote != nil && s.Remote.Port > 0 {
			port = s.Remote.Port
		}
		if a.remotePort != nil {
			port = *a.remotePort
		}
	}
	token, err := server.NewToken(16)
	if err != nil {
		a.errorNotice(err)
		return
	}
	host := a.remoteHost
	if host == "" {
		host = "0.0.0.0"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		a.errorNotice(fmt.Errorf("remote control: %w", err))
		return
	}
	r := &remote{token: token, addr: ln.Addr().String(), thread: a.sess.ID}
	r.api = server.NewLive(Version, remoteSession{a, r})
	r.api.OnClients = func(n int) {
		a.ui.Do(func() {
			if a.remote == r {
				r.clients = n
			}
		})
	}
	r.srv = &http.Server{Handler: r.api.HTTPHandler(token), ReadHeaderTimeout: 10 * time.Second}
	r.links = server.WebLinks(r.addr, token)
	if len(r.links) == 0 { // no network beyond this machine
		r.links = []string{"http://" + r.addr + "/#token=" + token}
	}
	r.last = a.remoteInfo(r)
	r.goal = a.remoteGoalInfo()
	a.remote = r
	go func() { _ = r.srv.Serve(ln) }()
	a.showRemote(r)
}

// stopRemote closes the server and every connection to it.
func (a *App) stopRemote() {
	r := a.remote
	if r == nil {
		return
	}
	a.remote = nil
	_ = r.srv.Close()
	r.api.Close()
}

// showRemote prints the links, a QR code of the first, and the warnings.
func (a *App) showRemote(r *remote) {
	b := &remoteBlock{title: "Remote control is on. Open this link on your phone or in a browser:", links: r.links}
	if qr, err := server.QR(r.links[0]); err == nil {
		b.qr = qr
	}
	if len(r.links) > 1 {
		b.hint = append(b.hint, "Other addresses of this machine are listed too; the QR code is the first.")
	}
	if !server.IsLoopback(r.addr) {
		b.hint = append(b.hint, strings.TrimPrefix(server.TLSWarning, "warning: "))
	}
	b.hint = append(b.hint, "Anyone with the link controls this session. /remote off stops it and revokes the link.")
	a.add(b)
}

// remoteBlock shows the links and QR code of /remote.
type remoteBlock struct {
	title string
	links []string
	qr    []string
	hint  []string
}

func (b *remoteBlock) Render(width int) []string {
	out := []string{"  " + tui.Bold("◉ ") + b.title}
	for _, l := range b.links {
		for _, w := range tui.Wrap(l, max(1, width-4)) {
			out = append(out, "    "+tui.FG(6, w))
		}
	}
	if len(b.qr) > 0 {
		if qw := tui.VisibleWidth(b.qr[0]) + 4; qw <= width {
			out = append(out, "")
			for _, l := range b.qr {
				out = append(out, "    "+l) // not styled: the code needs plain foreground blocks
			}
		} else {
			out = append(out, "  "+tui.Dim(fmt.Sprintf("(widen the terminal to %d columns for the QR code)", qw)))
		}
	}
	for _, h := range b.hint {
		for _, w := range tui.Wrap(h, max(1, width-2)) {
			out = append(out, "  "+tui.Dim(w))
		}
	}
	return out
}

// remoteStatus is the status line's indicator.
func (a *App) remoteStatus() string {
	if a.remote == nil {
		return ""
	}
	return tui.Dim(fmt.Sprintf("remote · %d connected", a.remote.clients))
}

// --- what the App tells the clients ---

func (a *App) remoteInfo(r *remote) server.ThreadInfo {
	m, effort := a.agent.Current()
	info := server.ThreadInfo{
		ID: a.sess.ID, Cwd: a.cwd, Name: a.sessName, Effort: effort, Efforts: m.Model.Levels(),
		ContextWindow: m.Model.ContextWindow, ContextTokens: a.ctxTokens, Busy: a.busy, TurnID: r.turnID, Live: true,
	}
	if m.Model.ID != "" {
		info.Model = m.ProviderName + "/" + m.Model.ID
	}
	return info
}

func (a *App) remotePublish(method string, params map[string]any) {
	r := a.remote
	if r == nil {
		return
	}
	params["threadId"] = a.sess.ID
	r.api.Publish(method, params)
}

// remoteItem publishes a transcript item change; replays are not sent, a
// switch notification follows them.
func (a *App) remoteItem(method string, it *transcript.Item) {
	if a.remote == nil || a.replaying {
		return
	}
	a.remotePublish(method, map[string]any{"turnId": a.remote.turnID, "item": server.WireItem(it)})
}

func (a *App) remoteDelta(it *transcript.Item, d string) {
	if a.remote == nil || a.replaying {
		return
	}
	a.remotePublish("item/delta", map[string]any{"turnId": a.remote.turnID, "itemId": it.ID, "delta": d})
}

// remoteNotice sends a notice of the terminal (not a transcript item) as
// a finished notice item; thread/read has it too, until the session is
// switched.
func (a *App) remoteNotice(text string) {
	r := a.remote
	if r == nil {
		return
	}
	r.notices++
	it := server.Item{ID: fmt.Sprintf("%s-n%d", a.sess.ID, r.notices), Type: server.ItemNotice, Text: text, Status: "completed"}
	if len(r.notes) < maxRemoteNotes {
		r.notes = append(r.notes, remoteNote{len(a.tr().Items()), it})
	}
	a.remotePublish("item/completed", map[string]any{"turnId": r.turnID, "item": it})
}

// remoteSwitched tells clients the transcript was replaced: a new or
// resumed session, or another branch of this one.
func (a *App) remoteSwitched() {
	r := a.remote
	if r == nil {
		return
	}
	prev := r.thread
	r.thread = a.sess.ID
	r.last = a.remoteInfo(r)
	r.goal = a.remoteGoalInfo()
	r.notes = nil
	r.api.Publish("thread/switched", map[string]any{"threadId": a.sess.ID, "previousThreadId": prev})
}

// remoteUpdated publishes the thread's state when the model, effort or
// name changed.
func (a *App) remoteUpdated() {
	r := a.remote
	if r == nil {
		return
	}
	info := a.remoteInfo(r)
	if info.Model == r.last.Model && info.Effort == r.last.Effort && info.Name == r.last.Name && info.ID == r.last.ID {
		return
	}
	r.last = info
	a.remotePublish("thread/updated", map[string]any{"thread": info})
}

func (a *App) remoteTurnStarted() {
	r := a.remote
	if r == nil {
		return
	}
	r.turnSeq++
	r.turnID = fmt.Sprintf("%s-t%d", a.sess.ID, r.turnSeq)
	a.remotePublish("turn/started", map[string]any{"turnId": r.turnID})
	a.remoteGoal()
}

// remoteGoalInfo is the goal as the status line shows it; nil without one.
func (a *App) remoteGoalInfo() *server.GoalInfo {
	g := a.goal.Goal
	if g == nil {
		return nil
	}
	secs := a.goal.Elapsed()
	tokens := goal.Tokens(g.TokensUsed)
	if g.Budget > 0 {
		tokens += " / " + goal.Tokens(g.Budget)
	}
	return &server.GoalInfo{
		Objective: g.Objective, Status: string(g.Status), Label: g.Status.Label(),
		Indicator: g.Indicator(secs), Summary: g.Summary(), Note: g.Note,
		Tokens: tokens, TokensUsed: g.TokensUsed, Budget: g.Budget,
		Elapsed: goal.FormatElapsed(secs), Seconds: secs,
	}
}

// remoteGoal publishes the goal when it changed (goal/updated; null when
// cleared). It is called where the goal changes, and by the inbox's tick
// for the time of a running turn.
func (a *App) remoteGoal() {
	r := a.remote
	if r == nil {
		return
	}
	g := a.remoteGoalInfo()
	if (g == nil) == (r.goal == nil) && (g == nil || *g == *r.goal) {
		return
	}
	r.goal = g
	a.remotePublish("goal/updated", map[string]any{"goal": g})
}

func (a *App) remoteTurnCompleted(err error) {
	r := a.remote
	if r == nil {
		return
	}
	status, msg := "completed", ""
	switch {
	case errors.Is(err, context.Canceled):
		status = "interrupted"
	case err != nil:
		status, msg = "failed", err.Error()
	}
	params := map[string]any{"turnId": r.turnID, "status": status, "contextTokens": a.ctxTokens}
	if msg != "" {
		params["error"] = msg
	}
	r.turnID = ""
	a.remotePublish("turn/completed", params)
	a.remoteUpdated()
}

// --- what the clients ask of the App ---

// remoteSession is the App as the server's live thread. Every method runs
// under the UI lock, and fails once its /remote server was stopped.
type remoteSession struct {
	a *App
	r *remote
}

func (l remoteSession) do(fn func() error) error {
	err := errRemoteOff
	l.a.ui.Do(func() {
		if l.a.remote == l.r {
			err = fn()
		}
	})
	return err
}

func (l remoteSession) Thread(items bool, at func()) (server.ThreadInfo, error) {
	var info server.ThreadInfo
	err := l.do(func() error {
		info = l.a.remoteInfo(l.r)
		info.Goal = l.a.remoteGoalInfo()
		if p := l.a.prompt; p != nil {
			w := p.wire
			info.Prompt = &w
		}
		if items {
			notes := l.r.notes
			for i, it := range l.a.tr().Items() {
				for len(notes) > 0 && notes[0].at <= i {
					info.Items, notes = append(info.Items, notes[0].item), notes[1:]
				}
				info.Items = append(info.Items, server.WireItem(&it))
			}
			for _, n := range notes {
				info.Items = append(info.Items, n.item)
			}
		}
		if at != nil {
			at()
		}
		return nil
	})
	return info, err
}

func (l remoteSession) Model() config.ModelRef {
	var m config.ModelRef
	_ = l.do(func() error {
		m = l.a.model()
		return nil
	})
	return m
}

func (l remoteSession) Send(input string, imgs []provider.Image) (status, turnID string, err error) {
	a := l.a
	err = l.do(func() error {
		if why := a.sess.ReadOnly(); why != "" {
			return errors.New(why)
		}
		if a.model().Model.ID == "" && !strings.HasPrefix(input, "/") {
			return errors.New("no model is set up yet: use /login or /model in the terminal")
		}
		var att []tui.Attachment
		for _, im := range imgs {
			att = append(att, tui.Attachment{Label: images.Label(im), Value: im})
		}
		wasBusy, kind, queued, steers := a.busy, a.runKind, len(a.queued), len(a.pendingSteers)
		a.fromRemote = true
		a.submit(input, att)
		a.fromRemote = false
		switch {
		case !wasBusy && a.busy:
			status, turnID = "started", l.r.turnID
		case wasBusy && kind == "turn" && len(a.pendingSteers) > steers:
			status = "steered"
			if a.remoteSteers == nil {
				a.remoteSteers = map[string]int{}
			}
			a.remoteSteers[input]++
		case len(a.queued) > queued:
			status = "queued"
		default:
			status = "done" // a command that ran at once
		}
		return nil
	})
	return status, turnID, err
}

func (l remoteSession) Interrupt() bool {
	ok := false
	_ = l.do(func() error {
		ok = l.a.interrupt()
		return nil
	})
	return ok
}

func (l remoteSession) Background() bool {
	ok := false
	_ = l.do(func() error {
		ok = l.a.busy && l.a.agent.Background()
		return nil
	})
	return ok
}

func (l remoteSession) SetModel(id string) (server.ThreadInfo, error) {
	var info server.ThreadInfo
	err := l.do(func() error {
		ref, ok := l.a.models.Find("", id)
		if !ok {
			return fmt.Errorf("unknown model %q", id)
		}
		l.a.setModel(ref)
		info = l.a.remoteInfo(l.r)
		return nil
	})
	return info, err
}

func (l remoteSession) SetEffort(level string) (server.ThreadInfo, error) {
	var info server.ThreadInfo
	err := l.do(func() error {
		levels := l.a.efforts()
		found := false
		for _, x := range levels {
			found = found || x == level
		}
		if !found {
			return fmt.Errorf("unknown effort %q (levels: %s)", level, strings.Join(levels, ", "))
		}
		l.a.setEffort(level, true)
		info = l.a.remoteInfo(l.r)
		return nil
	})
	return info, err
}

// takeRemoteSteer reports whether text was steered from the remote, and
// forgets it.
func (a *App) takeRemoteSteer(text string) bool {
	if a.remoteSteers[text] == 0 {
		return false
	}
	a.remoteSteers[text]--
	if a.remoteSteers[text] == 0 {
		delete(a.remoteSteers, text)
	}
	return true
}
