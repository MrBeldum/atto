package images

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// PastedPath turns pasted text into a file path if it looks like one image
// path, as terminals produce when a file is dropped on them. Following
// codex-rs (normalize_pasted_path), it accepts quoted paths, file:// URLs,
// Windows and UNC paths, and shell-escaped paths ("My\ Shot.png").
func PastedPath(pasted string) (string, bool) {
	s := strings.TrimSpace(pasted)
	if s == "" || strings.ContainsAny(s, "\n\r") {
		return "", false
	}
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	} else if !windowsPath(s) {
		s = unescapeShell(s)
	}
	if strings.HasPrefix(s, "file://") {
		u, err := url.Parse(s)
		if err != nil || (u.Host != "" && u.Host != "localhost") {
			return "", false
		}
		s = u.Path
		if runtime.GOOS == "windows" {
			s = filepath.FromSlash(strings.TrimPrefix(s, "/"))
		}
	}
	if !imageExt(s) {
		return "", false
	}
	return s, true
}

func windowsPath(s string) bool {
	drive := len(s) >= 3 && isLetter(s[0]) && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
	return drive || strings.HasPrefix(s, `\\`)
}

func isLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// unescapeShell removes backslash escapes the way a POSIX shell would for
// a single unquoted word.
func unescapeShell(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func imageExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}
