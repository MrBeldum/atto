package tui

// Ported from pi (https://github.com/earendil-works/pi), Copyright (c) 2025
// Mario Zechner, MIT License; see THIRD_PARTY_NOTICES.

import (
	"strings"
	"unicode"
)

// "@" mentions: typing @ at the start of a token opens a list of project
// files, as in pi. These are the text rules; the app supplies the files.

// pathWrappers are opening brackets that may come before a path in prose,
// with their closers: "(@src/main.go)" still completes.
var pathWrappers = map[rune]rune{'(': ')', '[': ']', '{': '}', '<': '>', '`': '`'}

// isAutocompleteSep reports whether r separates prose from a completion:
// whitespace or CJK punctuation (CJK letters stay part of words and paths).
func isAutocompleteSep(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	if strings.ContainsRune("，．：；！？（）［］｛｝“”‘’…—", r) {
		return true
	}
	return unicode.IsPunct(r) && (r >= 0x3000 && r <= 0x303f || r >= 0xff00 && r <= 0xffef ||
		unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.Bopomofo))
}

func isPathDelimiter(r rune) bool {
	return r == ' ' || r == '\t' || r == '"' || r == '\'' || r == '=' || isAutocompleteSep(r)
}

// isTokenStart reports whether a token starting at text[i] starts a word,
// past any opening wrappers before it.
func isTokenStart(text []rune, i int) bool {
	for i > 0 && pathWrappers[text[i-1]] != 0 {
		i--
	}
	return i == 0 || isPathDelimiter(text[i-1])
}

// stripLeadingWrappers drops opening wrappers before a path ("(@src" is
// "@src"), but keeps one the token also closes ("[slug]/pa").
func stripLeadingWrappers(tok []rune) []rune {
	for len(tok) > 0 {
		closer := pathWrappers[tok[0]]
		if closer == 0 || runeIndex(tok[1:], []rune{closer}) >= 0 {
			break
		}
		tok = tok[1:]
	}
	return tok
}

// MentionPrefix returns the "@path" or `@"quoted path` token that text (the
// line up to the cursor) ends with, if any: an @ that starts a token, not
// one inside a word such as an email address.
func MentionPrefix(text string) (string, bool) {
	rs := []rune(text)
	// An unclosed quote opened by @" at a token start.
	quote := -1
	for i, r := range rs {
		if r == '"' {
			if quote < 0 {
				quote = i
			} else {
				quote = -1
			}
		}
	}
	if quote > 0 && rs[quote-1] == '@' && isTokenStart(rs, quote-1) {
		return string(rs[quote-1:]), true
	}
	last := -1
	for i, r := range rs {
		if isPathDelimiter(r) {
			last = i
		}
	}
	tok := stripLeadingWrappers(rs[last+1:])
	if len(tok) > 0 && tok[0] == '@' {
		return string(tok), true
	}
	return "", false
}

// ParseMention returns the path typed in a mention prefix and whether it
// is quoted.
func ParseMention(prefix string) (query string, quoted bool) {
	if q, ok := strings.CutPrefix(prefix, `@"`); ok {
		return q, true
	}
	return strings.TrimPrefix(prefix, "@"), false
}

// MentionValue is the text a mention of path completes to: "@path", or
// `@"path"` when the prefix was quoted or the path has a space in it. A
// directory's path ends in "/".
func MentionValue(path string, quoted bool) string {
	if quoted || strings.IndexFunc(path, isAutocompleteSep) >= 0 {
		return `@"` + path + `"`
	}
	return "@" + path
}

// CompleteMention is how accepting value (from MentionValue) replaces
// prefix, the token before the cursor, given the text after the cursor:
// the runes to delete before and after the cursor, the text to insert, and
// where in it the cursor ends. A file gets a trailing space; a directory
// does not, and keeps the cursor inside its quotes, so completing goes on.
func CompleteMention(prefix, after, value string, dir bool) (before, afterDel int, insert string, cursor int) {
	before = len([]rune(prefix))
	if strings.HasPrefix(prefix, `@"`) && strings.HasSuffix(value, `"`) && strings.HasPrefix(after, `"`) {
		afterDel = 1 // the closing quote is already there
	}
	insert, cursor = value, len([]rune(value))
	if dir {
		if strings.HasSuffix(value, `"`) {
			cursor--
		}
	} else {
		insert += " "
		cursor++
	}
	return before, afterDel, insert, cursor
}
