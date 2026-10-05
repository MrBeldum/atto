// Package config locates atto's user directory (~/.atto) and its files.
//
// Layout mirrors pi's ~/.pi/agent, flattened since atto has a single binary:
//
//	~/.atto/
//	  settings.json   user settings
//	  models.json     providers and models
//	  auth.json       API keys / credentials
//	  sessions/       session transcripts
//	  extensions/     hooks and extensions
//	  prompts/        prompt templates
//	  skills/         skills
//	  themes/         custom themes
//	  bin/            helper binaries
//
// ATTO_DIR overrides the root.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const EnvDir = "ATTO_DIR"

// Dir returns the atto root directory.
func Dir() string {
	if d := os.Getenv(EnvDir); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".atto"
	}
	return filepath.Join(home, ".atto")
}

func SettingsPath() string  { return filepath.Join(Dir(), "settings.json") }
func ModelsPath() string    { return filepath.Join(Dir(), "models.json") }
func AuthPath() string      { return filepath.Join(Dir(), "auth.json") }
func SessionsDir() string   { return filepath.Join(Dir(), "sessions") }
func ArchivedDir() string   { return filepath.Join(Dir(), "archived_sessions") }
func ExtensionsDir() string { return filepath.Join(Dir(), "extensions") }
func PromptsDir() string    { return filepath.Join(Dir(), "prompts") }
func SkillsDir() string     { return filepath.Join(Dir(), "skills") }
func ThemesDir() string     { return filepath.Join(Dir(), "themes") }
func BinDir() string        { return filepath.Join(Dir(), "bin") }

// Ensure creates the root and its subdirectories if missing.
func Ensure() error {
	for _, d := range []string{Dir(), SessionsDir(), ExtensionsDir(), PromptsDir(), SkillsDir(), ThemesDir(), BinDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Settings is the contents of settings.json. Unknown fields are ignored.
type Settings struct {
	DefaultProvider string `json:"defaultProvider,omitempty"`
	DefaultModel    string `json:"defaultModel,omitempty"`
	DefaultEffort   string `json:"defaultEffort,omitempty"`
	// Renderer is "fullscreen" (default) or "inline".
	Renderer string `json:"renderer,omitempty"`
	// StatusLine replaces the built-in status line with a command's output,
	// like Claude Code's statusLine setting.
	StatusLine *StatusLine `json:"statusLine,omitempty"`
}

// StatusLine configures a custom status line. The command runs with a JSON
// description of the session on stdin; each line it prints becomes a status
// line (ANSI colors allowed).
type StatusLine struct {
	Type    string `json:"type"` // "command"
	Command string `json:"command"`
	// RefreshInterval, in seconds, re-runs the command periodically even if
	// nothing changed. 0 runs it only on changes.
	RefreshInterval int `json:"refreshInterval,omitempty"`
}

// LoadSettings reads settings.json; a missing file yields zero settings.
func LoadSettings() (Settings, error) {
	var s Settings
	data, err := os.ReadFile(SettingsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// UpdateSettings sets the given top-level keys in settings.json, preserving
// any other keys already present.
func UpdateSettings(kv map[string]any) error {
	raw := map[string]any{}
	data, err := os.ReadFile(SettingsPath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
	}
	for k, v := range kv {
		raw[k] = v
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SettingsPath(), append(out, '\n'), 0o644)
}
