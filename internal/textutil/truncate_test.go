package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateRunesNeverSplitsARune(t *testing.T) {
	for _, limit := range []int{1, 7, 33, 99} {
		got := TruncateRunes(strings.Repeat("é", 200), limit)
		if !utf8.ValidString(got) {
			t.Fatalf("limit %d produced invalid UTF-8: %q", limit, got)
		}
		if n := utf8.RuneCountInString(got); n > limit {
			t.Fatalf("limit %d produced %d runes", limit, n)
		}
	}
}

func TestTruncateMiddleKeepsBothEndsAndCountsRunes(t *testing.T) {
	in := strings.Repeat("あ", 300)
	out, removed := TruncateMiddle(in, 51)
	if !utf8.ValidString(out) {
		t.Fatalf("invalid UTF-8: %q", out)
	}
	if removed != 300-51 {
		t.Fatalf("removed = %d, want %d", removed, 300-51)
	}
	if !strings.HasPrefix(out, "あ") || !strings.HasSuffix(out, "あ") {
		t.Fatalf("head or tail missing: %q", out)
	}
}

func TestTruncateShorterThanLimitIsUnchanged(t *testing.T) {
	if got := TruncateRunes("héllo", 50); got != "héllo" {
		t.Fatalf("got %q", got)
	}
	if out, removed := TruncateMiddle("héllo", 50); out != "héllo" || removed != 0 {
		t.Fatalf("got %q, %d", out, removed)
	}
}

func TestTruncateRunesCountsRunesNotBytes(t *testing.T) {
	if got := TruncateRunes("héllo wörld", 5); got != "héllo" {
		t.Fatalf("got %q, want %q", got, "héllo")
	}
	if got := TruncateRunes("abc", 0); got != "" {
		t.Fatalf("limit 0 got %q, want empty", got)
	}
	if got := TruncateRunes("abc", -3); got != "" {
		t.Fatalf("negative limit got %q, want empty", got)
	}
}

func TestTruncateMiddleSpendsTwoThirdsOnTheHead(t *testing.T) {
	in := "0123456789" + strings.Repeat("x", 80) + "abcdefghij"
	head, tail, removed := TruncateMiddleParts(in, 9)
	if head != "012345" {
		t.Fatalf("head = %q, want %q", head, "012345")
	}
	if tail != "hij" {
		t.Fatalf("tail = %q, want %q", tail, "hij")
	}
	if removed != 100-9 {
		t.Fatalf("removed = %d, want %d", removed, 100-9)
	}

	out, joined := TruncateMiddle(in, 9)
	if out != head+tail || joined != removed {
		t.Fatalf("TruncateMiddle = %q, %d; want %q, %d", out, joined, head+tail, removed)
	}
}

func TestTruncateMiddleWithoutABudgetKeepsNothing(t *testing.T) {
	out, removed := TruncateMiddle("héllo", 0)
	if out != "" || removed != 5 {
		t.Fatalf("got %q, %d; want %q, %d", out, removed, "", 5)
	}
}
