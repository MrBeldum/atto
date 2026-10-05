package extensions

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// The extensions that ship inside atto: each builtin/<name>.ts is one,
// written against the public API like any user's (diff.ts is /diff). They
// load with the others, source Builtin, without approval; settings.json can
// disable them by name, and a user or project extension of the same name
// replaces one.
//
// They are bundled from source at every start. That costs about a
// millisecond a file (see TestBuiltinStartupCost), so no pre-bundled copy
// is kept to go stale.
//
//go:embed builtin/*.ts
var builtinFS embed.FS

const builtinDir = "builtin"

// builtinSpecs are the built-in extensions, in name order. Path is virtual:
// a name for messages and stack traces, not a file on disk.
func builtinSpecs() []Spec {
	entries, _ := builtinFS.ReadDir(builtinDir)
	var out []Spec
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".ts"); ok && !e.IsDir() {
			out = append(out, Spec{Name: name, Path: path.Join(builtinDir, e.Name()), Source: Builtin})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// bundleSpec bundles the extension s: from disk, or for a built-in one
// from the binary. A built-in extension is a single file.
func bundleSpec(s Spec) (string, error) {
	if s.Source != Builtin {
		return Bundle(s.Path)
	}
	src, err := builtinFS.ReadFile(s.Path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", s.Path, err)
	}
	return bundle(api.BuildOptions{Stdin: &api.StdinOptions{
		Contents:   string(src),
		Sourcefile: s.Path,
		Loader:     api.LoaderTS,
	}}, s.Path)
}

// BuiltinSource is the TypeScript source of the built-in extension name.
func BuiltinSource(name string) (string, bool) {
	src, err := builtinFS.ReadFile(path.Join(builtinDir, name+".ts"))
	return string(src), err == nil
}
