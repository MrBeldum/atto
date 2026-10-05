package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/hooks"
	"github.com/sebastianrcnt/atto/provider"
	"github.com/sebastianrcnt/atto/session"
)

// Server holds threads and dispatches JSON-RPC requests. Notifications
// go to the Notify function (a transport fan-out).
type Server struct {
	Version string
	Cwd     string // default working directory for new threads
	Notify  func(method string, params map[string]any)

	mu      sync.Mutex
	threads map[string]*thread
}

func New(version, cwd string) *Server {
	return &Server{Version: version, Cwd: cwd, threads: map[string]*thread{}, Notify: func(string, map[string]any) {}}
}

type thread struct {
	mu        sync.Mutex
	id        string
	cwd       string
	name      string
	agent     *agent.Agent
	sess      *session.Writer
	hooks     *hooks.Runner
	items     []Item
	busy      bool
	turnID    string
	cancel    context.CancelFunc
	itemSeq   int
	turnSeq   int
	ctxTokens int
	usage     provider.Usage // totals for the running turn
}

func (t *thread) nextItemID() string {
	t.itemSeq++
	return fmt.Sprintf("%s-i%d", t.id, t.itemSeq)
}

func (t *thread) info() ThreadInfo {
	m, effort := t.agent.Current()
	return ThreadInfo{
		ID: t.id, Cwd: t.cwd, Name: t.name, Model: m.ProviderName + "/" + m.Model.ID, Effort: effort,
		Efforts: m.Model.Levels(), ContextWindow: m.Model.ContextWindow, ContextTokens: t.ctxTokens,
		Busy: t.busy, TurnID: t.turnID,
	}
}

// --- dispatch ---

// Handle processes one JSON-RPC message and returns the response (nil for
// notifications sent by the client).
func (s *Server) Handle(ctx context.Context, raw []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, err.Error()}}
	}
	result, err := s.call(ctx, req.Method, req.Params)
	if req.ID == nil {
		return nil
	}
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	var rerr *rpcError
	switch {
	case errors.As(err, &rerr):
		resp.Error = rerr
	case err != nil:
		resp.Error = &rpcError{codeServer, err.Error()}
	default:
		if result == nil {
			result = map[string]any{}
		}
		resp.Result = result
	}
	return resp
}

func (e *rpcError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &rpcError{codeInvalidParams, fmt.Sprintf(format, args...)}
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return v, invalid("%v", err)
		}
	}
	return v, nil
}

type threadParams struct {
	ThreadID string `json:"threadId"`
	Cwd      string `json:"cwd"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
	Input    string `json:"input"`
	Archived bool   `json:"archived"`
}

func (s *Server) call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	p, err := decode[threadParams](raw)
	if err != nil {
		return nil, err
	}
	switch method {
	case "initialize":
		return map[string]any{"name": "atto", "version": s.Version, "protocolVersion": ProtocolVersion}, nil
	case "models/list":
		return s.listModels()
	case "thread/start":
		return s.startThread(p)
	case "thread/resume":
		return s.resumeThread(p.ThreadID)
	case "thread/read":
		t, err := s.thread(p.ThreadID)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		info := t.info()
		info.Items = append([]Item(nil), t.items...)
		return info, nil
	case "thread/list":
		return s.listThreads(p)
	case "thread/setModel":
		return s.setModel(p)
	case "thread/setEffort":
		return s.setEffort(p)
	case "thread/compact":
		return s.startCompact(p.ThreadID)
	case "turn/start":
		return s.startTurn(p)
	case "turn/steer":
		t, err := s.thread(p.ThreadID)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		busy := t.busy
		t.mu.Unlock()
		if !busy {
			return nil, &rpcError{codeServer, "no turn is running; use turn/start"}
		}
		if strings.TrimSpace(p.Input) == "" {
			return nil, invalid("input is required")
		}
		t.agent.Steer(p.Input)
		return nil, nil
	case "turn/interrupt":
		t, err := s.thread(p.ThreadID)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		if t.cancel != nil {
			t.cancel()
		}
		t.mu.Unlock()
		return nil, nil
	}
	return nil, &rpcError{codeMethodNotFound, "unknown method " + method}
}

func (s *Server) thread(id string) (*thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.threads[id]
	if t == nil {
		return nil, invalid("unknown thread %q (thread/start or thread/resume first)", id)
	}
	return t, nil
}

// --- threads ---

func loadConfig() (config.ModelsFile, config.Settings, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return config.ModelsFile{}, settings, err
	}
	models, err := config.LoadModels()
	return models, settings, err
}

func (s *Server) listModels() (any, error) {
	models, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range models.List() {
		out = append(out, map[string]any{
			"id": r.ProviderName + "/" + r.Model.ID, "name": r.Model.DisplayName(),
			"contextWindow": r.Model.ContextWindow, "efforts": r.Model.Levels(), "hasKey": r.APIKey != "" || len(r.Provider.Env) == 0,
		})
	}
	return map[string]any{"models": out}, nil
}

func pickModel(models config.ModelsFile, settings config.Settings, id string) (config.ModelRef, error) {
	if id != "" {
		if r, ok := models.Find("", id); ok {
			return r, nil
		}
		return config.ModelRef{}, invalid("unknown model %q", id)
	}
	if r, ok := models.Find(settings.DefaultProvider, settings.DefaultModel); ok {
		return r, nil
	}
	all := models.List()
	if len(all) == 0 {
		return config.ModelRef{}, fmt.Errorf("no models configured")
	}
	return all[0], nil
}

func sessionEnv(id string) []string {
	env := []string{"ATTO_SESSION_ID=" + id}
	if exe, err := os.Executable(); err == nil {
		env = append(env, "PATH="+dirOf(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, os.PathSeparator); i > 0 {
		return p[:i]
	}
	return "."
}

// newThread wires an agent, session writer and hooks for cwd.
func (s *Server) newThread(cwd string, model config.ModelRef, effort string, sess *session.Writer, start time.Time) (*thread, error) {
	ag := agent.New(model, effort, cwd)
	ag.SetStart(start)
	ag.SetSession(sess.ID, sessionEnv(sess.ID))
	ag.Record = sess.Append
	t := &thread{id: sess.ID, cwd: cwd, agent: ag, sess: sess}
	hookCfg, err := config.LoadHooks(cwd)
	if err != nil {
		return nil, err
	}
	if t.hooks = hooks.New(hookCfg, cwd); t.hooks != nil {
		t.hooks.SetSession(sess.ID, sess.Path)
		ag.Hooks = t.hooks
	}
	s.mu.Lock()
	s.threads[t.id] = t
	s.mu.Unlock()
	return t, nil
}

func (s *Server) startThread(p threadParams) (any, error) {
	models, settings, err := loadConfig()
	if err != nil {
		return nil, err
	}
	model, err := pickModel(models, settings, p.Model)
	if err != nil {
		return nil, err
	}
	effort := p.Effort
	if effort == "" {
		effort = settings.DefaultEffort
	}
	cwd := p.Cwd
	if cwd == "" {
		cwd = s.Cwd
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, invalid("cwd %q is not a directory", cwd)
	}
	t, err := s.newThread(cwd, model, effort, session.New(cwd), time.Now())
	if err != nil {
		return nil, err
	}
	s.sessionStart(t, "startup")
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info(), nil
}

func (s *Server) sessionStart(t *thread, source string) {
	if t.hooks == nil {
		return
	}
	go func() {
		for _, n := range t.hooks.SessionStart(context.Background(), source) {
			s.notify(t, "hook", map[string]any{"event": "SessionStart", "message": n})
		}
	}()
}

func (s *Server) resumeThread(id string) (any, error) {
	s.mu.Lock()
	existing := s.threads[id]
	s.mu.Unlock()
	if existing != nil { // already loaded: same as read
		existing.mu.Lock()
		defer existing.mu.Unlock()
		info := existing.info()
		info.Items = append([]Item(nil), existing.items...)
		return info, nil
	}
	path, err := session.Find(id)
	if err != nil {
		return nil, invalid("%v", err)
	}
	h, entries, err := session.Load(path)
	if err != nil {
		return nil, err
	}
	models, settings, err := loadConfig()
	if err != nil {
		return nil, err
	}
	model, err := pickModel(models, settings, "")
	if err != nil {
		return nil, err
	}
	effort, name := settings.DefaultEffort, ""
	for _, e := range entries {
		switch e.Type {
		case session.TypeModel:
			if r, ok := models.Find(e.Provider, e.Model); ok {
				model = r
			}
		case session.TypeEffort:
			effort = e.Effort
		case session.TypeName:
			name = e.Name
		}
	}
	t, err := s.newThread(h.Cwd, model, effort, session.Resume(path, h), h.Time)
	if err != nil {
		return nil, err
	}
	t.agent.Restore(entries)
	t.name = name
	t.ctxTokens = t.agent.ContextTokens()
	t.items = ItemsFromEntries(t.id, entries)
	t.itemSeq = len(t.items)
	s.sessionStart(t, "resume")
	t.mu.Lock()
	defer t.mu.Unlock()
	info := t.info()
	info.Items = append([]Item(nil), t.items...)
	return info, nil
}

func (s *Server) listThreads(p threadParams) (any, error) {
	list, err := session.List(p.Cwd, p.Archived)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, x := range list {
		s.mu.Lock()
		_, loaded := s.threads[x.ID]
		s.mu.Unlock()
		out = append(out, map[string]any{
			"threadId": x.ID, "name": x.Name, "preview": x.Preview, "cwd": x.Cwd,
			"updatedAt": x.Updated, "messages": x.Messages, "loaded": loaded,
		})
	}
	return map[string]any{"threads": out}, nil
}

func (s *Server) setModel(p threadParams) (any, error) {
	t, err := s.thread(p.ThreadID)
	if err != nil {
		return nil, err
	}
	models, settings, err := loadConfig()
	if err != nil {
		return nil, err
	}
	model, err := pickModel(models, settings, p.Model)
	if err != nil {
		return nil, err
	}
	t.agent.SetModel(model)
	t.sess.Append(session.Entry{Type: session.TypeModel, Provider: model.ProviderName, Model: model.Model.ID})
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info(), nil
}

func (s *Server) setEffort(p threadParams) (any, error) {
	t, err := s.thread(p.ThreadID)
	if err != nil {
		return nil, err
	}
	m, _ := t.agent.Current()
	if lv := m.Model.Levels(); len(lv) > 0 && !contains(lv, p.Effort) {
		return nil, invalid("effort %q not available (levels: %s)", p.Effort, strings.Join(lv, ", "))
	}
	t.agent.SetEffort(p.Effort)
	t.sess.Append(session.Entry{Type: session.TypeEffort, Effort: p.Effort})
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info(), nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// --- turns ---

func (s *Server) notify(t *thread, method string, params map[string]any) {
	params["threadId"] = t.id
	s.Notify(method, params)
}

// begin marks the thread busy and starts fn in the background.
func (s *Server) begin(t *thread, fn func(ctx context.Context, emit func(any)) error) (string, error) {
	t.mu.Lock()
	if t.busy {
		t.mu.Unlock()
		return "", &rpcError{codeServer, "a turn is already running; use turn/steer or turn/interrupt"}
	}
	t.turnSeq++
	turnID := fmt.Sprintf("%s-t%d", t.id, t.turnSeq)
	ctx, cancel := context.WithCancel(context.Background())
	t.busy, t.turnID, t.cancel, t.usage = true, turnID, cancel, provider.Usage{}
	t.mu.Unlock()

	s.notify(t, "turn/started", map[string]any{"turnId": turnID})
	m := &itemMapper{s: s, t: t, turnID: turnID}
	go func() {
		err := fn(ctx, m.event)
		m.closeOpen()
		t.mu.Lock()
		t.busy, t.cancel, t.turnID = false, nil, ""
		t.ctxTokens = t.agent.ContextTokens()
		usage, ctxTokens := t.usage, t.ctxTokens
		t.mu.Unlock()
		cancel()
		status, msg := "completed", ""
		switch {
		case errors.Is(err, context.Canceled):
			status = "interrupted"
		case err != nil:
			status, msg = "failed", err.Error()
		}
		params := map[string]any{"turnId": turnID, "status": status, "contextTokens": ctxTokens,
			"usage": map[string]int{"inputTokens": usage.PromptTokens, "cachedInputTokens": usage.CachedTokens, "outputTokens": usage.CompletionTokens}}
		if msg != "" {
			params["error"] = msg
		}
		s.notify(t, "turn/completed", params)
	}()
	return turnID, nil
}

func (s *Server) startTurn(p threadParams) (any, error) {
	t, err := s.thread(p.ThreadID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Input) == "" {
		return nil, invalid("input is required")
	}
	var turnID string
	turnID, err = s.begin(t, func(ctx context.Context, emit func(any)) error {
		emit(userInput{p.Input})
		return t.agent.Run(ctx, p.Input, emit)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"turnId": turnID}, nil
}

func (s *Server) startCompact(id string) (any, error) {
	t, err := s.thread(id)
	if err != nil {
		return nil, err
	}
	turnID, err := s.begin(t, t.agent.Compact)
	if err != nil {
		return nil, err
	}
	return map[string]any{"turnId": turnID}, nil
}

// userInput is a synthetic event so the turn's user message becomes an item.
type userInput struct{ text string }
