package session

import (
	"os"
	"path/filepath"
	"strings"
)

// GitBranch returns the branch checked out in the repository containing
// dir, "HEAD" when detached, and "" outside a repository. It reads .git/HEAD
// rather than running git, so it is cheap enough for every new session.
func GitBranch(dir string) string {
	for d := filepath.Clean(dir); ; {
		gitdir := filepath.Join(d, ".git")
		if st, err := os.Stat(gitdir); err == nil {
			if !st.IsDir() { // a worktree or submodule: ".git" is a file pointing at the real one
				b, err := os.ReadFile(gitdir)
				if err != nil {
					return ""
				}
				target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
				if !ok {
					return ""
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(d, target)
				}
				gitdir = target
			}
			head, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
			if err != nil {
				return ""
			}
			if ref, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/"); ok {
				return ref
			}
			return "HEAD"
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}
