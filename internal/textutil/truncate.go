// Package textutil cuts text to a budget measured in runes.
//
// A byte budget applied to non-ASCII text is wrong twice over: it keeps far
// less than it promises, and it can end mid-rune, which JSON encoding then
// rewrites as U+FFFD. Every limit here counts runes and every cut lands on a
// rune boundary, so the output is always valid UTF-8.
package textutil

import "unicode/utf8"

// TruncateRunes returns the first limit runes of s. A limit of zero or less
// keeps nothing.
func TruncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	// No rune is shorter than a byte, so a string within the byte budget is
	// already within the rune budget.
	if len(s) <= limit {
		return s
	}
	n := 0
	for i := range s {
		if n == limit {
			return s[:i]
		}
		n++
	}
	return s
}

// TruncateMiddleParts keeps a head and a tail of s within limit runes, drops
// everything between them, and reports how many runes it dropped. The budget
// goes two thirds to the head and one third to the tail. Callers that mark the
// seam need the two pieces; TruncateMiddle is the same cut already joined.
func TruncateMiddleParts(s string, limit int) (head, tail string, removed int) {
	total := utf8.RuneCountInString(s)
	if limit <= 0 {
		return "", "", total
	}
	if total <= limit {
		return s, "", 0
	}
	headRunes := limit * 2 / 3
	return TruncateRunes(s, headRunes), lastRunes(s, limit-headRunes), total - limit
}

// TruncateMiddle keeps the head and tail of s within limit runes and drops the
// middle, returning the kept text and how many runes it removed so a caller can
// say how much is missing.
func TruncateMiddle(s string, limit int) (out string, removed int) {
	head, tail, removed := TruncateMiddleParts(s, limit)
	return head + tail, removed
}

// lastRunes returns the final limit runes of s.
func lastRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	n := 0
	for i := len(s); i > 0; {
		_, size := utf8.DecodeLastRuneInString(s[:i])
		i -= size
		n++
		if n == limit {
			return s[i:]
		}
	}
	return s
}
