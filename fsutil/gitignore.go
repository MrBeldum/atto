package fsutil

import (
	"os"
	"regexp"
	"strings"
)

// ignoreRule is one .gitignore pattern. base is the directory of the file
// it came from, as a slash path from the repository root ("" for the root).
type ignoreRule struct {
	base    string
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// ignoredBy applies rules in order, git's way: the last matching pattern
// decides, so a later "!keep.log" re-includes what "*.log" excluded. path
// is a slash path from the repository root.
func ignoredBy(rules []ignoreRule, path string, dir bool) bool {
	ignored := false
	for _, r := range rules {
		if r.dirOnly && !dir {
			continue
		}
		rel := path
		if r.base != "" {
			var ok bool
			if rel, ok = strings.CutPrefix(path, r.base+"/"); !ok {
				continue
			}
		}
		if r.re.MatchString(rel) {
			ignored = !r.negate
		}
	}
	return ignored
}

// readIgnore parses the ignore file at file, if there is one, for patterns
// relative to base.
func readIgnore(file, base string) []ignoreRule {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	return parseIgnore(string(data), base)
}

// parseIgnore parses .gitignore text (gitignore(5)): comments, "!"
// negation, a trailing "/" for directories only, patterns anchored to
// base when they contain a "/", and *, ?, [...] and ** globs.
func parseIgnore(text, base string) []ignoreRule {
	var rules []ignoreRule
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\r\n")
		// Trailing spaces are dropped unless escaped.
		for strings.HasSuffix(line, " ") && !strings.HasSuffix(line, `\ `) {
			line = line[:len(line)-1]
		}
		if line == "" || line[0] == '#' {
			continue
		}
		r := ignoreRule{base: base}
		if line[0] == '!' {
			r.negate, line = true, line[1:]
		} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly, line = true, strings.TrimRight(line, "/")
		}
		if line == "" {
			continue
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		var b strings.Builder
		b.WriteString("^")
		if !anchored {
			b.WriteString("(?:.*/)?") // a bare name matches at any depth
		}
		globRegexp(&b, line)
		b.WriteString("$")
		re, err := regexp.Compile(b.String())
		if err != nil {
			continue
		}
		r.re = re
		rules = append(rules, r)
	}
	return rules
}

// globRegexp writes the regular expression for a gitignore glob.
func globRegexp(b *strings.Builder, g string) {
	for i := 0; i < len(g); i++ {
		switch c := g[i]; {
		case strings.HasPrefix(g[i:], "**/") && (i == 0 || g[i-1] == '/'):
			b.WriteString("(?:.*/)?") // any number of directories, even none
			i += 2
		case g[i:] == "**" && i > 0 && g[i-1] == '/':
			b.WriteString(".*") // everything inside
			i++
		case c == '*':
			b.WriteString("[^/]*")
			for i+1 < len(g) && g[i+1] == '*' {
				i++
			}
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			end := strings.IndexByte(g[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := g[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			i += end + 1
		case c == '\\' && i+1 < len(g):
			i++
			b.WriteString(regexp.QuoteMeta(g[i : i+1]))
		default:
			b.WriteString(regexp.QuoteMeta(g[i : i+1]))
		}
	}
}
