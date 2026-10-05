package skills

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"

	"github.com/sebastianrcnt/atto/fsutil"
)

// The skills that ship inside atto: builtin/<name>/SKILL.md, one file each.
// The model reads a skill with the shell tool, so the file has to exist on
// disk: LoadBuiltin writes each one to <cache>/<hash>/<name>/SKILL.md, where
// <hash> covers the content of all of them (a new atto with new text gets a
// new directory; an old one is never rewritten under a running session).
// A built-in skill is therefore a single file; there are no relative paths
// to resolve.
//
//go:embed builtin/*/SKILL.md
var builtinFS embed.FS

type builtinFile struct {
	name string
	data []byte
}

func builtinFiles() []builtinFile {
	entries, _ := builtinFS.ReadDir("builtin")
	var out []builtinFile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := builtinFS.ReadFile(path.Join("builtin", e.Name(), "SKILL.md"))
		if err == nil {
			out = append(out, builtinFile{e.Name(), data})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// BuiltinNames lists the built-in skills, sorted.
func BuiltinNames() []string {
	var out []string
	for _, f := range builtinFiles() {
		out = append(out, f.name)
	}
	return out
}

// LoadBuiltin returns the built-in skills in name order, materialized under
// cacheDir, except those named in disabled. A skill that cannot be written
// is left out with an Issue. The result is a function of the binary and
// disabled alone, so the system prompt stays stable.
func LoadBuiltin(cacheDir string, disabled []string) (out []Skill, issues []Issue) {
	files := builtinFiles()
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00", f.name, len(f.data))
		h.Write(f.data)
	}
	root := filepath.Join(cacheDir, hex.EncodeToString(h.Sum(nil))[:12])
	for _, f := range files {
		if slices.Contains(disabled, f.name) {
			continue
		}
		p := filepath.Join(root, f.name, "SKILL.md")
		if cur, err := os.ReadFile(p); err != nil || !bytes.Equal(cur, f.data) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
				err = fsutil.WriteAtomic(p, f.data, 0o644)
			}
			if _, err := os.Stat(p); err != nil {
				issues = append(issues, Issue{Path: p, Reason: "built-in skill " + f.name + " not written: " + err.Error(), Skipped: true})
				continue
			}
		}
		fm, _, err := ParseFrontmatter(string(f.data))
		if err != nil {
			issues = append(issues, Issue{Path: p, Reason: err.Error(), Skipped: true})
			continue
		}
		name := fm["name"]
		if name == "" {
			name = f.name
		}
		out = append(out, Skill{
			Name: name, Description: fm["description"], FilePath: p, BaseDir: filepath.Dir(p),
			DisableModelInvocation: fm["disable-model-invocation"] == "true", Source: Builtin,
		})
	}
	return out, issues
}

// WithBuiltin appends the built-in skills to found, after the ones from
// directories: a skill of the same name from a directory wins and the
// built-in one is dropped silently. disabled is settings.json's skills.disabled.
func WithBuiltin(found []Skill, cacheDir string, disabled []string) ([]Skill, []Issue) {
	b, issues := LoadBuiltin(cacheDir, disabled)
	for _, s := range b {
		if !slices.ContainsFunc(found, func(o Skill) bool { return o.Name == s.Name }) {
			found = append(found, s)
		}
	}
	return found, issues
}
