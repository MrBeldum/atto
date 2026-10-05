// Package core is what every front end (the TUI, atto -p and the server)
// does to run a conversation: load the configuration, pick the model and
// effort, build the agent and hooks for a directory, bind them to a session
// file, restore what a saved session last used, and clean up when leaving
// it. GoalDriver keeps a goal going across turns, and core/transcript
// turns the agent's events and saved sessions into the items they show.
// Front ends own their display and input; the steps live here once.
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sebastianrcnt/atto/agent"
	"github.com/sebastianrcnt/atto/config"
	"github.com/sebastianrcnt/atto/events"
	"github.com/sebastianrcnt/atto/goal"
	"github.com/sebastianrcnt/atto/hooks"
	"github.com/sebastianrcnt/atto/jobs"
	"github.com/sebastianrcnt/atto/session"
)

// DefaultEffort applies when neither a flag, the session nor settings.json
// says otherwise.
const DefaultEffort = "medium"

// Load creates ~/.atto if needed and reads settings.json and models.json.
func Load() (config.Settings, config.ModelsFile, error) {
	if err := config.Ensure(); err != nil {
		return config.Settings{}, config.ModelsFile{}, err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return settings, config.ModelsFile{}, fmt.Errorf("%s: %w", config.SettingsPath(), err)
	}
	models, err := config.LoadModels()
	if err != nil {
		return settings, models, fmt.Errorf("%s: %w", config.ModelsPath(), err)
	}
	return settings, models, nil
}

// ErrNoModels means no provider has credentials or models configured. The
// TUI starts anyway and shows NoModelsHint; atto -p fails with it.
var ErrNoModels error = noModelsError{}

type noModelsError struct{}

func (noModelsError) Error() string { return NoModelsHint() }

// NoModelsHint tells a first-time user how to get a model (pi:
// formatNoModelsAvailableMessage).
func NoModelsHint() string {
	return "No models available. Use /login (or run: atto login) to sign in or save an API key, or add a provider to " + config.ModelsPath() + "."
}

// PickModel resolves id (provider/id or a bare id). Without one it takes
// the default from settings.json, else the first configured model.
func PickModel(models config.ModelsFile, settings config.Settings, id string) (config.ModelRef, error) {
	if id != "" {
		if r, ok := models.Find("", id); ok {
			return r, nil
		}
		return config.ModelRef{}, fmt.Errorf("unknown model %q (see: atto models)", id)
	}
	if r, ok := models.Find(settings.DefaultProvider, settings.DefaultModel); ok {
		return r, nil
	}
	all := models.List()
	if len(all) == 0 {
		return config.ModelRef{}, ErrNoModels
	}
	return all[0], nil
}

// Effort returns the first effort set: the given one, settings.json's
// default, else DefaultEffort.
func Effort(settings config.Settings, given string) string {
	switch {
	case given != "":
		return given
	case settings.DefaultEffort != "":
		return settings.DefaultEffort
	}
	return DefaultEffort
}

// CheckEffort reports an effort the model doesn't offer.
func CheckEffort(m config.ModelRef, effort string) error {
	if lv := m.Model.Levels(); len(lv) > 0 && !slices.Contains(lv, effort) {
		return fmt.Errorf("%s has no effort %q (levels: %s)", m.Model.ID, effort, strings.Join(lv, ", "))
	}
	return nil
}

// NewAgent builds an agent for cwd with the hooks configured for it. The
// hooks runner is nil when there are none.
func NewAgent(cwd string, model config.ModelRef, effort string) (*agent.Agent, *hooks.Runner, error) {
	ag := agent.New(model, effort, cwd)
	cfg, err := config.LoadHooks(cwd)
	if err != nil {
		return nil, nil, err
	}
	hk := hooks.New(cfg, cwd)
	if hk != nil {
		ag.Hooks = hk
	}
	return ag, hk, nil
}

// Bind points ag and hk at a session file. start fixes the date in the
// system prompt (a resumed session keeps its own, which keeps the prefix
// cache); record makes the agent append its messages to the file.
func Bind(ag *agent.Agent, hk *hooks.Runner, file *session.Writer, start time.Time, record bool) {
	ag.SetStart(start)
	ag.SetSession(file.ID, Env(file.ID))
	ag.Record = nil
	if record {
		ag.Record = file.Append
	}
	hk.SetSession(file.ID, file.Path)
}

// Env is the environment of the commands an agent runs: the session ID
// (for atto history, job, goal...), the ATTO_AGENT guard, and the atto
// binary's directory first on PATH.
func Env(id string) []string {
	env := []string{"ATTO_SESSION_ID=" + id, config.EnvAgent + "=1"}
	if exe, err := os.Executable(); err == nil {
		env = append(env, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return env
}

// Saved is what a session file says to restore beyond its messages: the
// last model, effort and name. They are session-wide, so the latest value
// wins whichever branch it was recorded on.
type Saved struct {
	Header  session.Entry
	Entries []session.Entry
	Model   string // provider/id
	Effort  string
	Name    string
}

// Open loads a saved session and reopens its file for appending, on the
// branch it was left on.
func Open(path string) (Saved, *session.Writer, error) {
	h, entries, err := session.Load(path)
	if err != nil {
		return Saved{}, nil, err
	}
	s := Saved{Header: h, Entries: entries}
	for _, e := range entries {
		switch e.Type {
		case session.TypeModel:
			s.Model = e.Provider + "/" + e.Model
		case session.TypeEffort:
			s.Effort = e.Effort
		case session.TypeName:
			s.Name = e.Name
		}
	}
	file := session.Resume(path, h)
	file.SetLeaf(session.Leaf(entries))
	return s, file, nil
}

// Branch is the active branch, which is what the agent restores.
func (s Saved) Branch() []session.Entry { return session.Active(s.Entries) }

// Leave cleans up after a session: its background jobs end with it (as in
// codex) and its goal file goes (the session file keeps the goal's last
// snapshot). Returns how many jobs were stopped.
func Leave(id string) int {
	_ = goal.Clear(id)
	return jobs.KillAll(id)
}

// Poll fires the session's due timers and takes the events waiting in its
// inbox (finished jobs, monitors, timers).
func Poll(id string) []events.Event {
	events.FireDue(id, time.Now())
	return events.Drain(id)
}
