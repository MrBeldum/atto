package extensions

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// target is the newest syntax goja runs unchanged. goja has async/await
// natively but no async generators (ES2018), so esbuild lowers those and
// everything newer (class fields, ??, ?., private members...).
const target = api.ES2017

// Bundle compiles entry, a TypeScript or JavaScript file, together with
// the files it imports into one CommonJS script that goja runs. Types are
// erased, not checked. The script carries an inline source map, so stack
// traces name the original files and lines. Errors read
// "file:line:col: message", one per line.
func Bundle(entry string) (string, error) {
	abs, err := filepath.Abs(entry)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(abs)
	res := api.Build(api.BuildOptions{
		EntryPoints:   []string{abs},
		AbsWorkingDir: dir,
		Bundle:        true,
		Write:         false,
		Format:        api.FormatCommonJS,
		Platform:      api.PlatformNeutral,
		Target:        target,
		Sourcemap:     api.SourceMapInline,
		// The maps only need lines; the sources are on disk.
		SourcesContent: api.SourcesContentExclude,
		LogLevel:       api.LogLevelSilent,
		Charset:        api.CharsetUTF8,
	})
	if len(res.Errors) > 0 {
		var lines []string
		for _, m := range res.Errors {
			lines = append(lines, formatMessage(dir, m))
		}
		return "", fmt.Errorf("%s", strings.Join(lines, "\n"))
	}
	if len(res.OutputFiles) == 0 {
		return "", fmt.Errorf("%s: esbuild wrote nothing", entry)
	}
	return string(res.OutputFiles[0].Contents), nil
}

func formatMessage(dir string, m api.Message) string {
	if m.Location == nil {
		return m.Text
	}
	file := m.Location.File
	if !filepath.IsAbs(file) {
		file = filepath.Join(dir, file)
	}
	return fmt.Sprintf("%s:%d:%d: %s", file, m.Location.Line, m.Location.Column+1, m.Text)
}

// hash identifies a bundle's code: approving a project extension approves
// this hash, so a change to it or to a file it imports needs approval
// again.
func hash(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}
