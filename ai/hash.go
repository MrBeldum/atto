package ai

import (
	"strconv"
	"unicode/utf16"
)

// ShortHash is a port of src/utils/hash.ts: a fast deterministic hash to
// shorten long strings. It iterates UTF-16 code units like JavaScript's
// charCodeAt, so the result equals pi's for the same input.
func ShortHash(s string) string {
	h1, h2 := uint32(0xdeadbeef), uint32(0x41c6ce57)
	for _, ch := range utf16.Encode([]rune(s)) {
		h1 = (h1 ^ uint32(ch)) * 2654435761
		h2 = (h2 ^ uint32(ch)) * 1597334677
	}
	h1 = (h1^(h1>>16))*2246822507 ^ (h2^(h2>>13))*3266489909
	h2 = (h2^(h2>>16))*2246822507 ^ (h1^(h1>>13))*3266489909
	return strconv.FormatUint(uint64(h2), 36) + strconv.FormatUint(uint64(h1), 36)
}
