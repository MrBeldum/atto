package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/term"

	"atto/agent"
	"atto/config"
	"atto/provider"
	"atto/session"
)

// PrintOptions configures a non-interactive run (atto -p).
type PrintOptions struct {
	Prompt   string
	Model    string // provider/id; default model if empty
	Effort   string // default effort if empty
	Format   string // "text" (default), "json" or "stream-json"
	Partial  bool   // stream-json: also emit text/reasoning deltas
	Verbose  bool   // text: show tool activity on stderr
	MaxSteps int    // stop after this many model calls (0: unlimited)
	Continue bool   // continue the latest session in this directory
	Resume   string // continue the session with this ID
	NoSave   bool   // do not record the run as a session
}

// printResult is the final JSON object for --output-format json and the
// last line of stream-json.
type printResult struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype"` // "success" or "error"
	IsError    bool   `json:"is_error"`
	Result     string `json:"result"`
	Error      string `json:"error,omitempty"`
	SessionID  string `json:"session_id"`
	Model      string `json:"model"`
	NumSteps   int    `json:"num_steps"`
	DurationMs int64  `json:"duration_ms"`
	Usage      struct {
		InputTokens       int `json:"input_tokens"`
		CachedInputTokens int `json:"cached_input_tokens"`
		OutputTokens      int `json:"output_tokens"`
	} `json:"usage"`
}

// ErrPrintFailed signals a non-zero exit after output was already written.
var ErrPrintFailed = errors.New("run failed")

// ReadPromptInput combines the prompt arguments with piped stdin, as
// `cat file | atto -p "explain this"` does.
func ReadPromptInput(args []string) (string, error) {
	prompt := strings.TrimSpace(strings.Join(args, " "))
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 16<<20))
		if err != nil {
			return "", err
		}
		if in := strings.TrimSpace(string(data)); in != "" {
			if prompt == "" {
				prompt = in
			} else {
				prompt += "\n\n" + in
			}
		}
	}
	if prompt == "" {
		return "", fmt.Errorf("no prompt: pass it as an argument or on stdin")
	}
	return prompt, nil
}

// RunPrint runs one prompt without the TUI. Assistant text goes to stdout;
// in text mode tool activity goes to stderr with -v.
func RunPrint(o PrintOptions) error {
	if err := config.Ensure(); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	models, err := config.LoadModels()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// Session: new, latest (-c) or by ID (--resume).
	var sess *session.Writer
	var entries []session.Entry
	var start time.Time
	switch {
	case o.Resume != "" || o.Continue:
		path := ""
		if o.Resume != "" {
			if path, err = session.Find(o.Resume); err != nil {
				return err
			}
		} else if s, ok := session.Latest(cwd); ok {
			path = s.Path
		} else {
			return fmt.Errorf("no previous session in this directory")
		}
		var h session.Entry
		if h, entries, err = session.Load(path); err != nil {
			return err
		}
		sess, start = session.Resume(path, h), h.Time
	default:
		sess, start = session.New(cwd), time.Now()
	}
	defer sess.Close()

	// Model and effort: flags, else the session's last, else defaults.
	modelID, effort := o.Model, o.Effort
	if modelID == "" || effort == "" {
		for _, e := range entries {
			if e.Type == session.TypeModel && modelID == "" {
				modelID = e.Provider + "/" + e.Model
			}
			if e.Type == session.TypeEffort && o.Effort == "" {
				effort = e.Effort
			}
		}
	}
	model, ok := pickModel(models, settings, modelID)
	if !ok && o.Model != "" {
		return fmt.Errorf("unknown model %q (see: atto models)", o.Model)
	}
	if !ok {
		all := models.List()
		if len(all) == 0 {
			return fmt.Errorf("no models configured; add a provider to %s", config.ModelsPath())
		}
		model = all[0]
	}
	if effort == "" {
		effort = settings.DefaultEffort
	}
	if effort == "" {
		effort = "medium"
	}
	if lv := model.Model.Levels(); len(lv) > 0 && !contains(lv, effort) {
		return fmt.Errorf("%s has no effort %q (levels: %s)", model.Model.ID, effort, strings.Join(lv, ", "))
	}

	ag := agent.New(model, effort, cwd)
	ag.SetStart(start)
	ag.SetSession(sess.ID, sessionEnv(sess.ID))
	ag.MaxSteps = o.MaxSteps
	ag.Restore(entries)
	if !o.NoSave {
		ag.Record = sess.Append
		sess.Append(session.Entry{Type: session.TypeModel, Provider: model.ProviderName, Model: model.Model.ID})
		sess.Append(session.Entry{Type: session.TypeEffort, Effort: ag.Effort()})
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p := &printer{format: o.Format, partial: o.Partial, verbose: o.Verbose, out: os.Stdout, errOut: os.Stderr}
	res := printResult{Type: "result", SessionID: sess.ID, Model: model.ProviderName + "/" + model.Model.ID}
	p.emit(map[string]any{"type": "init", "session_id": sess.ID, "model": res.Model, "effort": ag.Effort(), "cwd": cwd})

	began := time.Now()
	runErr := ag.Run(ctx, o.Prompt, func(ev any) { p.event(ev, &res) })
	p.flushStep()
	res.DurationMs = time.Since(began).Milliseconds()
	res.Result = strings.TrimSpace(p.lastText)
	res.Subtype = "success"
	if runErr != nil {
		res.Subtype, res.IsError, res.Error = "error", true, runErr.Error()
	}

	switch o.Format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	case "stream-json":
		p.emit(res)
	default:
		if p.wroteText {
			fmt.Fprintln(os.Stdout)
		}
		if runErr != nil {
			fmt.Fprintln(os.Stderr, "atto:", runErr)
		}
	}
	if runErr != nil {
		return ErrPrintFailed
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// printer renders agent events for one output format.
type printer struct {
	format  string
	partial bool
	verbose bool
	out     io.Writer
	errOut  io.Writer

	// Current step, flushed as one "assistant" event in stream-json.
	text, reasoning strings.Builder
	lastText        string
	wroteText       bool
	tools           map[string]agent.BashArgs
}

func (p *printer) emit(v any) {
	if p.format != "stream-json" {
		return
	}
	b, _ := json.Marshal(v)
	fmt.Fprintf(p.out, "%s\n", b)
}

func (p *printer) flushStep() {
	if p.text.Len() == 0 && p.reasoning.Len() == 0 {
		return
	}
	if t := strings.TrimSpace(p.text.String()); t != "" {
		p.lastText = t
	}
	p.emit(map[string]any{"type": "assistant", "text": p.text.String(), "reasoning": p.reasoning.String()})
	p.text.Reset()
	p.reasoning.Reset()
}

func (p *printer) event(ev any, res *printResult) {
	if p.tools == nil {
		p.tools = map[string]agent.BashArgs{}
	}
	switch e := ev.(type) {
	case agent.ReasoningDelta:
		p.reasoning.WriteString(e.Text)
		if p.partial {
			p.emit(map[string]any{"type": "delta", "kind": "reasoning", "text": e.Text})
		}
	case agent.TextDelta:
		p.text.WriteString(e.Text)
		if p.partial {
			p.emit(map[string]any{"type": "delta", "kind": "text", "text": e.Text})
		}
		if p.format == "" || p.format == "text" {
			fmt.Fprint(p.out, e.Text)
			p.wroteText = true
		}
	case agent.ToolStart:
		p.tools[e.ID] = e.Args
		p.flushStep()
		p.emit(map[string]any{"type": "tool_use", "id": e.ID, "description": e.Args.Description, "command": e.Args.Command})
		if p.verbose && (p.format == "" || p.format == "text") {
			fmt.Fprintf(p.errOut, "\n● %s  $ %s\n", e.Args.Description, firstLine(e.Args.Command))
		}
	case agent.ToolEnd:
		r := e.Result
		args := p.tools[e.ID]
		out := r.ForModel(args)
		if len(out) > 4000 {
			out = out[:4000] + "\n[truncated]"
		}
		p.emit(map[string]any{
			"type": "tool_result", "id": e.ID, "description": args.Description, "exit_code": r.ExitCode,
			"timed_out": r.TimedOut, "duration_ms": r.Duration.Milliseconds(), "output": out,
		})
		if p.verbose && (p.format == "" || p.format == "text") {
			status := fmt.Sprintf("exit %d", r.ExitCode)
			if r.TimedOut {
				status = "timed out"
			}
			fmt.Fprintf(p.errOut, "  └ %s · %s\n", status, fmtDur(r.Duration))
		}
	case agent.StepEnd:
		p.flushStep()
		res.NumSteps++
		addUsage(res, e.Usage)
	case agent.CompactEnd:
		p.emit(map[string]any{"type": "compaction", "tokens_before": e.Before, "tokens_after": e.After})
	}
}

func addUsage(res *printResult, u provider.Usage) {
	res.Usage.InputTokens += u.PromptTokens
	res.Usage.CachedInputTokens += u.CachedTokens
	res.Usage.OutputTokens += u.CompletionTokens
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i] + " …"
		}
	}
	return s
}

// pickModel resolves -m (provider/id or id), else the default model.
func pickModel(models config.ModelsFile, settings config.Settings, modelID string) (config.ModelRef, bool) {
	if modelID != "" {
		return models.Find("", modelID)
	}
	return models.Find(settings.DefaultProvider, settings.DefaultModel)
}
