package tui

// Ported from pi (https://github.com/earendil-works/pi), Copyright (c) 2025
// Mario Zechner, MIT License; see THIRD_PARTY_NOTICES.

import (
	"slices"
	"strings"
)

// FuzzyMatch reports whether every rune of query appears in text, in order
// and ignoring case, and scores the match: lower is better. Consecutive runs
// and matches at word starts (after space, -, _, ., / or :) score better,
// gaps and late matches worse, and an exact match best of all. A query such
// as "v2" or "2v" whose letters and digits are swapped also matches, a
// little worse, like pi's fuzzyMatch.
func FuzzyMatch(query, text string) (bool, float64) {
	q := []rune(strings.ToLower(query))
	t := []rune(strings.ToLower(text))
	if ok, score := fuzzyScore(q, t); ok {
		return true, score
	}
	if sw := swapAlnum(q); sw != nil {
		if ok, score := fuzzyScore(sw, t); ok {
			return true, score + 5
		}
	}
	return false, 0
}

func fuzzyScore(q, t []rune) (bool, float64) {
	if len(q) == 0 {
		return true, 0
	}
	if len(q) > len(t) {
		return false, 0
	}
	score, last, run := 0.0, -1, 0
	for _, c := range q {
		i := last + 1
		for i < len(t) && t[i] != c {
			i++
		}
		if i == len(t) {
			return false, 0
		}
		if last == i-1 {
			run++
			score -= float64(run * 5)
		} else {
			run = 0
			if last >= 0 {
				score += float64((i - last - 1) * 2)
			}
		}
		if i == 0 || strings.ContainsRune(" \t\n-_./:", t[i-1]) {
			score -= 10
		}
		score += float64(i) * 0.1
		last = i
	}
	if slices.Equal(q, t) {
		score -= 100
	}
	return true, score
}

// swapAlnum turns "abc12" into "12abc" and "12abc" into "abc12"; nil for
// other queries.
func swapAlnum(q []rune) []rune {
	letter := func(c rune) bool { return c >= 'a' && c <= 'z' }
	digit := func(c rune) bool { return c >= '0' && c <= '9' }
	for _, pair := range [2][2]func(rune) bool{{letter, digit}, {digit, letter}} {
		i := 0
		for i < len(q) && pair[0](q[i]) {
			i++
		}
		if i == 0 || i == len(q) {
			continue
		}
		rest := true
		for _, c := range q[i:] {
			rest = rest && pair[1](c)
		}
		if rest {
			return append(append([]rune{}, q[i:]...), q[:i]...)
		}
	}
	return nil
}
