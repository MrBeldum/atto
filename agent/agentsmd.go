package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sebastianrcnt/atto/config"
)

// maxInstructionBytes caps all AGENTS files together, like codex's
// project_doc_max_bytes: a stray huge file must not eat the context window.
const maxInstructionBytes = 32 * 1024

// instructionNames is the lookup order inside one directory (pi's file names,
// codex's override convention). The first regular file wins; the rest of that
// directory's candidates are ignored.
var instructionNames = []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"}

type instructionFile struct {
	path, text string
}

// projectRoot walks up from cwd to the first directory holding .git (a
// directory, or a file in worktrees and submodules), as codex does. Without
// one the project is just cwd.
func projectRoot(cwd string) string {
	for dir := cwd; ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

// instructionIn returns the first existing instruction file of dir.
func instructionIn(dir string) (instructionFile, bool) {
	for _, name := range instructionNames {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			return instructionFile{p, text}, true
		}
	}
	return instructionFile{}, false
}

// loadInstructions returns the global file first, then one file per directory
// from the project root down to cwd, so the most specific guidance comes last.
// It is read once per session start because the system prompt must not change
// mid-session (the prefix cache).
func loadInstructions(cwd string) []instructionFile {
	var out []instructionFile
	if f, ok := instructionIn(config.Dir()); ok {
		out = append(out, f)
	}
	var dirs []string
	for dir, root := cwd, projectRoot(cwd); ; {
		dirs = append([]string{dir}, dirs...)
		if dir == root || filepath.Dir(dir) == dir {
			break
		}
		dir = filepath.Dir(dir)
	}
	for _, dir := range dirs {
		if f, ok := instructionIn(dir); ok {
			out = append(out, f)
		}
	}
	return out
}

// writeInstructions appends the files to the system prompt within the byte
// budget, truncating the file that crosses it and dropping later ones.
func writeInstructions(b *strings.Builder, files []instructionFile) {
	remaining := maxInstructionBytes
	for i, f := range files {
		text, cut := f.text, false
		if len(text) > remaining {
			n := remaining
			for n > 0 && !utf8.RuneStart(text[n]) { // do not split a rune
				n--
			}
			text, cut = text[:n], true
		}
		fmt.Fprintf(b, "\n# Instructions from %s\n\n%s\n", f.path, text)
		remaining -= len(text)
		last := i+1 == len(files)
		if cut || (remaining == 0 && !last) {
			rest := "later files are"
			if cut && last {
				rest = "the rest of this file is"
			} else if cut {
				rest = "the rest of this file and later files are"
			}
			fmt.Fprintf(b, "\n[Truncated: instruction files exceed %d KiB; %s omitted.]\n", maxInstructionBytes/1024, rest)
			return
		}
	}
}
