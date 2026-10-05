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

// MaxInstructionKiB is that cap in KiB, for display.
const MaxInstructionKiB = maxInstructionBytes / 1024

// instructionNames is the lookup order inside one directory (pi's file names,
// codex's override convention). The first regular file wins; the rest of that
// directory's candidates are ignored.
var instructionNames = []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"}

type instructionFile struct {
	path, text string
}

// Instruction describes an AGENTS file in the system prompt.
type Instruction struct {
	Path  string
	Bytes int // of its text, trimmed
	// Kept is how much of it is in the prompt: less than Bytes when the
	// 32 KiB cap cut it, 0 when earlier files used up the cap.
	Kept int
	Text string
}

// SkippedInstruction is an instruction file that was found but is not in
// the prompt, and why.
type SkippedInstruction struct {
	Path, Reason string
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

// instructionIn returns the first existing instruction file of dir, and the
// other candidates it found there with why they are not used.
func instructionIn(dir string) (instructionFile, bool, []SkippedInstruction) {
	var skipped []SkippedInstruction
	var found instructionFile
	ok := false
	for _, name := range instructionNames {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			continue
		}
		if ok {
			skipped = append(skipped, SkippedInstruction{p, "shadowed by " + filepath.Base(found.path)})
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			skipped = append(skipped, SkippedInstruction{p, err.Error()})
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			found, ok = instructionFile{p, text}, true
		} else {
			skipped = append(skipped, SkippedInstruction{p, "empty"})
		}
	}
	return found, ok, skipped
}

// loadInstructions returns the global file first, then one file per directory
// from the project root down to cwd, so the most specific guidance comes last.
// It is read when a session starts (and on /reload), not per request,
// because the system prompt must not change under the prefix cache.
func loadInstructions(cwd string) []instructionFile {
	files, _ := scanInstructions(cwd)
	return files
}

// scanInstructions is loadInstructions with the files it passed over.
func scanInstructions(cwd string) ([]instructionFile, []SkippedInstruction) {
	var out []instructionFile
	var skipped []SkippedInstruction
	add := func(dir string) {
		f, ok, sk := instructionIn(dir)
		if ok {
			out = append(out, f)
		}
		skipped = append(skipped, sk...)
	}
	add(config.Dir())
	var dirs []string
	for dir, root := cwd, projectRoot(cwd); ; {
		dirs = append([]string{dir}, dirs...)
		if dir == root || filepath.Dir(dir) == dir {
			break
		}
		dir = filepath.Dir(dir)
	}
	for _, dir := range dirs {
		add(dir)
	}
	return out, skipped
}

// keptBytes is how much of each file fits the byte budget, in order: the
// file that crosses it is cut (on a rune boundary), later ones get nothing.
func keptBytes(files []instructionFile) []int {
	kept := make([]int, len(files))
	remaining := maxInstructionBytes
	for i, f := range files {
		n := len(f.text)
		if n > remaining {
			n = remaining
			for n > 0 && !utf8.RuneStart(f.text[n]) { // do not split a rune
				n--
			}
		}
		kept[i] = n
		remaining -= n
		if n < len(f.text) || remaining == 0 {
			break
		}
	}
	return kept
}

// describeInstructions reports files as the prompt holds them.
func describeInstructions(files []instructionFile) []Instruction {
	kept := keptBytes(files)
	out := make([]Instruction, len(files))
	for i, f := range files {
		out[i] = Instruction{Path: f.path, Bytes: len(f.text), Kept: kept[i], Text: f.text}
	}
	return out
}

// writeInstructions appends the files to the system prompt within the byte
// budget, truncating the file that crosses it and dropping later ones.
func writeInstructions(b *strings.Builder, files []instructionFile) {
	kept := keptBytes(files)
	remaining := maxInstructionBytes
	for i, f := range files {
		text := f.text[:kept[i]]
		cut := len(text) < len(f.text)
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
