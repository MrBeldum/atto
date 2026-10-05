package fsutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// tree makes files under root; a name ending in / is a directory.
func tree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func paths(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		p := e.Path
		if e.Dir {
			p += "/"
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func TestIgnorePatterns(t *testing.T) {
	rules := parseIgnore("# comment\n\n*.log\n!keep.log\nbuild/\n/top.txt\ndocs/*.md\n**/gen/**\na/**/z\n\\#hash\nsp\\ \n", "")
	for _, c := range []struct {
		path string
		dir  bool
		want bool
	}{
		{"x.log", false, true},
		{"deep/x.log", false, true},
		{"keep.log", false, false}, // negated after
		{"build", true, true},
		{"build", false, false}, // directories only
		{"sub/build", true, true},
		{"top.txt", false, true},
		{"sub/top.txt", false, false}, // anchored
		{"docs/a.md", false, true},
		{"docs/sub/a.md", false, false}, // * stops at /
		{"x/gen/y.go", false, true},
		{"gen/y.go", false, true},
		{"a/z", false, true},
		{"a/b/c/z", false, true},
		{"#hash", false, true},
		{"sp ", false, true},
		{"main.go", false, false},
	} {
		if got := ignoredBy(rules, c.path, c.dir); got != c.want {
			t.Errorf("%q (dir %v): ignored %v, want %v", c.path, c.dir, got, c.want)
		}
	}
	nested := parseIgnore("*.tmp\n/only\n", "sub")
	if !ignoredBy(nested, "sub/a/b.tmp", false) || ignoredBy(nested, "other/b.tmp", false) {
		t.Error("a nested .gitignore applies under its own directory only")
	}
	if !ignoredBy(nested, "sub/only", false) || ignoredBy(nested, "sub/a/only", false) {
		t.Error("anchored to the nested directory")
	}
}

func TestListFilesGitignore(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]string{
		".git/HEAD":            "ref",
		".git/info/exclude":    "secret.txt\n",
		".gitignore":           "node_modules/\n*.log\n",
		".hidden/conf":         "",
		"secret.txt":           "",
		"main.go":              "",
		"debug.log":            "",
		"node_modules/x/y.js":  "",
		"pkg/.gitignore":       "gen.go\n!keep.log\n",
		"pkg/gen.go":           "",
		"pkg/keep.log":         "",
		"pkg/lib.go":           "",
		"pkg/sub/gen.go":       "",
		"other/gen.go":         "",
		"empty/":               "",
		"with space/file a.md": "",
	})
	got, truncated := ListFiles(context.Background(), root, ListOptions{})
	want := []string{
		".gitignore", ".hidden/", ".hidden/conf", "empty/", "main.go",
		"other/", "other/gen.go", "pkg/", "pkg/.gitignore", "pkg/keep.log", "pkg/lib.go", "pkg/sub/",
		"with space/", "with space/file a.md",
	}
	if p := paths(got); !slices.Equal(p, want) || truncated {
		t.Fatalf("got %q (truncated %v)\nwant %q", p, truncated, want)
	}
	for i := 1; i < len(got); i++ {
		if strings.Count(got[i].Path, "/") < strings.Count(got[i-1].Path, "/") {
			t.Errorf("shallowest first: %q before %q", got[i-1].Path, got[i].Path)
		}
	}

	// From a subdirectory, the parents' rules still apply.
	sub, _ := ListFiles(context.Background(), filepath.Join(root, "pkg"), ListOptions{})
	if p := paths(sub); !slices.Equal(p, []string{".gitignore", "keep.log", "lib.go", "sub/"}) {
		t.Errorf("from pkg: %q", p)
	}
}

func TestListFilesCaps(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"a/b/c/d/e.txt": ""}
	for i := range 30 {
		files[fmt.Sprintf("f%02d.txt", i)] = ""
	}
	tree(t, root, files)
	got, truncated := ListFiles(context.Background(), root, ListOptions{MaxEntries: 10})
	if len(got) != 10 || !truncated {
		t.Fatalf("capped at 10: %d %v", len(got), truncated)
	}
	got, _ = ListFiles(context.Background(), root, ListOptions{MaxDepth: 2})
	if p := paths(got); slices.Contains(p, "a/b/c/") || !slices.Contains(p, "a/b/") {
		t.Errorf("depth 2 lists two levels: %q", p)
	}
	var calls int
	ListFiles(context.Background(), root, ListOptions{Progress: func([]Entry) { calls++ }})
	if calls != 0 {
		t.Errorf("progress comes every 500 entries, got %d calls", calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, _ := ListFiles(ctx, root, ListOptions{}); len(got) != 0 {
		t.Errorf("a cancelled walk stops: %d", len(got))
	}
	if got, _ := ListFiles(context.Background(), filepath.Join(root, "missing"), ListOptions{}); len(got) != 0 {
		t.Errorf("an unreadable root lists nothing: %d", len(got))
	}
}

func TestListFilesSymlinkLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	tree(t, root, map[string]string{"dir/f.txt": ""})
	tree(t, outside, map[string]string{"o.txt": ""})
	if err := os.Symlink(root, filepath.Join(root, "dir", "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(outside, "self")); err != nil {
		t.Fatal(err)
	}
	got, _ := ListFiles(context.Background(), root, ListOptions{})
	want := []string{"dir/", "dir/f.txt", "dir/loop/", "out/", "out/o.txt", "out/self/"}
	if p := paths(got); !slices.Equal(p, want) {
		t.Errorf("got %q, want %q", p, want)
	}
}
