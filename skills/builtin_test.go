package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinSkillsLoad(t *testing.T) {
	cache := t.TempDir()
	got, issues := LoadBuiltin(cache, nil)
	if len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
	if len(got) == 0 || got[0].Name != "atto-extensions" || got[0].Source != Builtin {
		t.Fatalf("skills: %+v", got)
	}
	for _, s := range got {
		if !strings.HasPrefix(s.FilePath, cache) || filepath.Base(s.FilePath) != "SKILL.md" || filepath.Dir(s.FilePath) != s.BaseDir {
			t.Errorf("%s: path %s", s.Name, s.FilePath)
		}
		if _, err := os.Stat(s.FilePath); err != nil {
			t.Errorf("%s not written: %v", s.Name, err)
		}
		// The shipped file passes the same validation as any skill.
		loaded, warns := Load([]string{filepath.Dir(s.BaseDir)})
		if len(warns) != 0 {
			t.Errorf("%s: %v", s.Name, warns)
		}
		found := false
		for _, l := range loaded {
			found = found || (l.Name == s.Name && l.Description == s.Description && len(l.Description) <= maxDescriptionLength)
		}
		if !found {
			t.Errorf("%s did not validate: %+v", s.Name, loaded)
		}
	}
	// The same binary gives the same paths and the same prompt block.
	again, _ := LoadBuiltin(cache, nil)
	if FormatForPrompt(got, "bash") != FormatForPrompt(again, "bash") {
		t.Error("prompt block is not deterministic")
	}
	// A damaged cache file is repaired.
	if err := os.WriteFile(got[0].FilePath, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	LoadBuiltin(cache, nil)
	if data, _ := os.ReadFile(got[0].FilePath); !strings.Contains(string(data), "name: atto-extensions") {
		t.Error("cache file not rewritten")
	}
}

func TestBuiltinOverrideAndDisable(t *testing.T) {
	cache, user := t.TempDir(), t.TempDir()
	write(t, filepath.Join(user, "atto-extensions", "SKILL.md"), "---\nname: atto-extensions\ndescription: Mine\n---\nx")
	write(t, filepath.Join(user, "other", "SKILL.md"), "---\nname: other\ndescription: Other\n---\nx")
	found, _ := Load([]string{user})
	all, _ := WithBuiltin(found, cache, nil)
	if len(all) != len(found)+len(BuiltinNames())-1 {
		t.Fatalf("skills: %s", names(all))
	}
	for _, s := range all {
		if s.Name == "atto-extensions" && (s.Description != "Mine" || s.Source != "") {
			t.Fatalf("the user's skill must win: %+v", s)
		}
	}

	plain, _ := WithBuiltin(nil, cache, nil)
	if n := names(plain); !strings.Contains(n, "atto-extensions") || n != strings.Join(BuiltinNames(), ",") {
		t.Fatalf("builtin only: %s", n)
	}
	off, issues := WithBuiltin(nil, cache, []string{"atto-extensions"})
	if len(off) != 0 || len(issues) != 0 {
		t.Fatalf("disabled: %v %v", off, issues)
	}
}
