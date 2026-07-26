package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Schedule {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNext(t *testing.T) {
	cases := []struct {
		expr string
		from string
		want string
	}{
		{"* * * * *", "2026-07-27 10:00", "2026-07-27 10:01"},
		{"0 8 * * *", "2026-07-27 10:00", "2026-07-28 08:00"},
		{"0 8 * * *", "2026-07-27 07:59", "2026-07-27 08:00"},
		{"*/15 * * * *", "2026-07-27 10:03", "2026-07-27 10:15"},
		{"30 2 1 * *", "2026-07-27 10:00", "2026-08-01 02:30"},
		{"0 0 * * mon", "2026-07-27 10:00", "2026-08-03 00:00"}, // 2026-07-27 is a Monday
		{"@daily", "2026-07-27 10:00", "2026-07-28 00:00"},
		{"@hourly", "2026-07-27 10:30", "2026-07-27 11:00"},
		{"0 9-17 * * *", "2026-07-27 08:00", "2026-07-27 09:00"},
		{"0 9-17 * * *", "2026-07-27 17:30", "2026-07-28 09:00"},
		{"0 0 1 jan *", "2026-07-27 10:00", "2027-01-01 00:00"},
	}
	for _, c := range cases {
		got := mustParse(t, c.expr).Next(at(c.from))
		if !got.Equal(at(c.want)) {
			t.Errorf("Parse(%q).Next(%s) = %s, want %s",
				c.expr, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

// Cron unions day-of-month and day-of-week when both are restricted.
func TestDayUnion(t *testing.T) {
	s := mustParse(t, "0 0 15 * fri")
	next := s.Next(at("2026-07-01 00:00"))
	if next.Day() != 3 || next.Weekday() != time.Friday {
		t.Fatalf("want the first Friday (3 Jul), got %s", next.Format("2006-01-02 Mon"))
	}
	next = s.Next(at("2026-07-13 00:00"))
	if next.Day() != 15 {
		t.Fatalf("want the 15th, got %s", next.Format("2006-01-02 Mon"))
	}
}

func TestEvery(t *testing.T) {
	s := mustParse(t, "@every 90m")
	got := s.Next(at("2026-07-27 10:00"))
	if !got.Equal(at("2026-07-27 11:30")) {
		t.Fatalf("got %s", got.Format("2006-01-02 15:04"))
	}
}

func TestParseErrors(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *",
		"* * 0 * *", "* * * 13 *", "* * * * 8", "a * * * *", "5-1 * * * *",
		"*/0 * * * *", "@every 30s",
	} {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) should have failed", expr)
		}
	}
}
