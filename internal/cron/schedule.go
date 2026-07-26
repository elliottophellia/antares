// Package cron implements the built-in scheduler: a five-field cron parser and
// a runner that executes natural-language jobs through the agent.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed five-field cron expression.
type Schedule struct {
	minutes  uint64 // bit i set => minute i (0-59)
	hours    uint32 // 0-23
	days     uint32 // 1-31
	months   uint16 // 1-12
	weekdays uint8  // 0-6, Sunday = 0
	// dayRestricted tracks whether day-of-month and day-of-week were both
	// specified, which cron treats as a union rather than an intersection.
	domSpecified bool
	dowSpecified bool
	// everyD is set for "@every <duration>" schedules, which bypass the fields.
	everyD time.Duration
	expr   string
}

// String returns the original expression.
func (s *Schedule) String() string { return s.expr }

var namedSchedules = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// Parse compiles a cron expression. It accepts the five-field form plus the
// common @daily style shorthands and "@every <duration>".
func Parse(expr string) (*Schedule, error) {
	original := strings.TrimSpace(expr)
	lower := strings.ToLower(original)

	if replacement, ok := namedSchedules[lower]; ok {
		s, err := Parse(replacement)
		if err != nil {
			return nil, err
		}
		s.expr = original
		return s, nil
	}
	if strings.HasPrefix(lower, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(lower, "@every ")))
		if err != nil {
			return nil, fmt.Errorf("invalid @every duration: %w", err)
		}
		if d < time.Minute {
			return nil, fmt.Errorf("@every interval must be at least 1 minute")
		}
		return &Schedule{expr: original, everyD: d}, nil
	}

	fields := strings.Fields(lower)
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected 5 fields (minute hour day month weekday), got %d", len(fields))
	}

	s := &Schedule{expr: original}
	var err error
	if s.minutes, err = parseField64(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	h, err := parseField64(fields[1], 0, 23, nil)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	s.hours = uint32(h)
	d, err := parseField64(fields[2], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	s.days = uint32(d)
	mo, err := parseField64(fields[3], 1, 12, monthNames)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	s.months = uint16(mo)
	w, err := parseField64(fields[4], 0, 6, dayNames)
	if err != nil {
		return nil, fmt.Errorf("weekday: %w", err)
	}
	s.weekdays = uint8(w)

	s.domSpecified = fields[2] != "*"
	s.dowSpecified = fields[4] != "*"
	return s, nil
}

// parseField64 expands one cron field into a bitmask.
func parseField64(field string, min, max int, names map[string]int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, fmt.Errorf("empty element")
		}

		step := 1
		if base, stepStr, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("invalid step %q", stepStr)
			}
			step = n
			part = base
		}

		lo, hi := min, max
		switch {
		case part == "*":
			// full range
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			var err error
			if lo, err = parseValue(a, min, max, names); err != nil {
				return 0, err
			}
			if hi, err = parseValue(b, min, max, names); err != nil {
				return 0, err
			}
			if lo > hi {
				return 0, fmt.Errorf("range %q is inverted", part)
			}
		default:
			v, err := parseValue(part, min, max, names)
			if err != nil {
				return 0, err
			}
			lo, hi = v, v
			if step > 1 {
				hi = max // "5/15" means "from 5 to the end, every 15"
			}
		}

		for i := lo; i <= hi; i += step {
			bits |= 1 << uint(i)
		}
	}
	if bits == 0 {
		return 0, fmt.Errorf("no values matched")
	}
	return bits, nil
}

func parseValue(s string, min, max int, names map[string]int) (int, error) {
	s = strings.TrimSpace(s)
	if names != nil {
		if v, ok := names[s]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	// Cron accepts 7 as Sunday.
	if max == 6 && v == 7 {
		v = 0
	}
	if v < min || v > max {
		return 0, fmt.Errorf("value %d is out of range %d-%d", v, min, max)
	}
	return v, nil
}

// Next returns the first activation strictly after t, or the zero time when the
// expression can never fire (searched up to five years ahead).
func (s *Schedule) Next(t time.Time) time.Time {
	if s.everyD > 0 {
		return t.Add(s.everyD).Truncate(time.Second)
	}

	// Start from the next whole minute.
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)

	for t.Before(limit) {
		if s.months&(1<<uint(t.Month())) == 0 {
			// Jump to the first day of the next month.
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 1, 0)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, 1)
			continue
		}
		if s.hours&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location()).Add(time.Hour)
			continue
		}
		if s.minutes&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// dayMatches applies cron's day-of-month / day-of-week union rule.
func (s *Schedule) dayMatches(t time.Time) bool {
	domOK := s.days&(1<<uint(t.Day())) != 0
	dowOK := s.weekdays&(1<<uint(t.Weekday())) != 0

	switch {
	case s.domSpecified && s.dowSpecified:
		return domOK || dowOK
	case s.domSpecified:
		return domOK
	case s.dowSpecified:
		return dowOK
	default:
		return true
	}
}
