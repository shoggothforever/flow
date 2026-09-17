// Package cron parses recurring time specifications used by flow's
// scheduler: standard 5-field cron expressions plus a small set of
// human-friendly "every" shortcuts.
//
// Supported "every" forms (case-insensitive):
//
//	every 30s          -> every 30 seconds (minimum granularity is 1s)
//	every 5m           -> every 5 minutes
//	every 2h           -> every 2 hours
//	@hourly            -> top of every hour
//	@daily             -> 00:00 every day
//	@weekly            -> 00:00 every Sunday
//	@midnight          -> alias for @daily
//	daily 09:30        -> at 09:30 every day
//	weekdays 18:00     -> Mon-Fri at 18:00
//	weekends 09:00     -> Sat+Sun at 09:00
//	mon 09:00          -> Monday at 09:00 (sun/mon/tue/wed/thu/fri/sat)
//
// Standard cron has 5 fields: minute hour day-of-month month day-of-week.
// We support: numbers, ranges (1-5), lists (1,3,5), steps (*/2, 1-10/3)
// and the * wildcard. Day names (sun..sat) and month names (jan..dec)
// are accepted in their respective columns.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule describes a recurring trigger. Use Parse to construct one.
type Schedule struct {
	// Either cron-derived bitmasks (when isCron) or interval-based fields.
	isCron bool

	// cron mode
	minute, hour, dom, month, dow uint64

	// interval mode (every Nm/h/s)
	interval time.Duration

	// fixed-time mode (daily/weekdays/dow)
	dailyHour, dailyMinute int
	dailyDow               uint8 // bitmask 0..6, 0=Sunday
}

// Parse accepts either a standard 5-field cron expression or one of the
// "every" shortcuts described in the package doc.
func Parse(spec string) (*Schedule, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return nil, fmt.Errorf("empty schedule")
	}

	// Predefined macros
	switch strings.ToLower(s) {
	case "@hourly":
		return Parse("0 * * * *")
	case "@daily", "@midnight":
		return Parse("0 0 * * *")
	case "@weekly":
		return Parse("0 0 * * 0")
	}

	// "every N{s,m,h}"
	if low := strings.ToLower(s); strings.HasPrefix(low, "every ") {
		return parseEvery(low[len("every "):])
	}

	// "daily HH:MM"
	if low := strings.ToLower(s); strings.HasPrefix(low, "daily ") {
		hh, mm, err := parseHHMM(strings.TrimPrefix(low, "daily "))
		if err != nil {
			return nil, err
		}
		return &Schedule{dailyHour: hh, dailyMinute: mm, dailyDow: 0x7F}, nil // all 7 days
	}
	// "weekdays HH:MM" / "weekends HH:MM"
	if low := strings.ToLower(s); strings.HasPrefix(low, "weekdays ") {
		hh, mm, err := parseHHMM(strings.TrimPrefix(low, "weekdays "))
		if err != nil {
			return nil, err
		}
		return &Schedule{dailyHour: hh, dailyMinute: mm, dailyDow: 0b0111110}, nil // Mon-Fri
	}
	if low := strings.ToLower(s); strings.HasPrefix(low, "weekends ") {
		hh, mm, err := parseHHMM(strings.TrimPrefix(low, "weekends "))
		if err != nil {
			return nil, err
		}
		return &Schedule{dailyHour: hh, dailyMinute: mm, dailyDow: 0b1000001}, nil // Sun+Sat
	}
	// "<dow> HH:MM" (sun/mon/tue/wed/thu/fri/sat)
	if dow, rest, ok := stripDayOfWeek(s); ok {
		hh, mm, err := parseHHMM(rest)
		if err != nil {
			return nil, err
		}
		return &Schedule{dailyHour: hh, dailyMinute: mm, dailyDow: 1 << dow}, nil
	}

	// Otherwise expect 5-field cron.
	return parseCron(s)
}

// Next returns the next trigger time strictly after `from`.
func (s *Schedule) Next(from time.Time) time.Time {
	if s.interval > 0 {
		// align to second granularity
		next := from.Add(s.interval).Truncate(time.Second)
		if !next.After(from) {
			next = next.Add(time.Second)
		}
		return next
	}
	if s.dailyDow != 0 {
		t := from.Truncate(time.Minute).Add(time.Minute)
		for i := 0; i < 8; i++ {
			candidate := time.Date(t.Year(), t.Month(), t.Day(), s.dailyHour, s.dailyMinute, 0, 0, t.Location())
			if candidate.After(from) && (s.dailyDow&(1<<uint(candidate.Weekday()))) != 0 {
				return candidate
			}
			t = t.AddDate(0, 0, 1)
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		}
		// unreachable in practice
		return from.Add(24 * time.Hour)
	}
	// cron mode: search minute by minute up to ~366 days.
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if s.matchesCron(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return from.Add(24 * time.Hour) // fallback
}

func (s *Schedule) matchesCron(t time.Time) bool {
	mo := uint64(1) << uint(t.Month())
	dom := uint64(1) << uint(t.Day())
	dow := uint64(1) << uint(t.Weekday())
	hr := uint64(1) << uint(t.Hour())
	mn := uint64(1) << uint(t.Minute())
	if (s.month & mo) == 0 {
		return false
	}
	if (s.hour & hr) == 0 {
		return false
	}
	if (s.minute & mn) == 0 {
		return false
	}
	// cron semantics: when both DOM and DOW are restricted, OR them; otherwise AND.
	domAll := s.dom == fullMask(1, 31)
	dowAll := s.dow == fullMask(0, 6)
	if !domAll && !dowAll {
		return (s.dom&dom) != 0 || (s.dow&dow) != 0
	}
	return (s.dom&dom) != 0 && (s.dow&dow) != 0
}

// String returns a stable representation of the schedule.
func (s *Schedule) String() string {
	if s.interval > 0 {
		return "every " + s.interval.String()
	}
	if s.dailyDow != 0 {
		switch s.dailyDow {
		case 0x7F:
			return fmt.Sprintf("daily %02d:%02d", s.dailyHour, s.dailyMinute)
		case 0b0111110:
			return fmt.Sprintf("weekdays %02d:%02d", s.dailyHour, s.dailyMinute)
		case 0b1000001:
			return fmt.Sprintf("weekends %02d:%02d", s.dailyHour, s.dailyMinute)
		}
	}
	return "<cron>"
}

// ----- helpers -----

func parseEvery(rest string) (*Schedule, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil, fmt.Errorf(`every: missing duration (e.g. "every 30s", "every 2h")`)
	}
	d, err := time.ParseDuration(rest)
	if err != nil {
		return nil, fmt.Errorf("every: invalid duration %q: %w", rest, err)
	}
	if d < time.Second {
		return nil, fmt.Errorf("every: minimum granularity is 1s, got %s", d)
	}
	return &Schedule{interval: d}, nil
}

func parseHHMM(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", s)
	}
	hh, err := strconv.Atoi(parts[0])
	if err != nil || hh < 0 || hh > 23 {
		return 0, 0, fmt.Errorf("invalid hour %q", parts[0])
	}
	mm, err := strconv.Atoi(parts[1])
	if err != nil || mm < 0 || mm > 59 {
		return 0, 0, fmt.Errorf("invalid minute %q", parts[1])
	}
	return hh, mm, nil
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

func stripDayOfWeek(s string) (dow int, rest string, ok bool) {
	low := strings.ToLower(s)
	for name, d := range dayNames {
		if strings.HasPrefix(low, name+" ") {
			return d, s[len(name)+1:], true
		}
	}
	return 0, "", false
}

// ----- cron parser -----

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

func parseCron(s string) (*Schedule, error) {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d (%q)", len(fields), s)
	}
	mn, err := parseField(fields[0], 0, 59, nil)
	if err != nil {
		return nil, fmt.Errorf("cron minute: %w", err)
	}
	hr, err := parseField(fields[1], 0, 23, nil)
	if err != nil {
		return nil, fmt.Errorf("cron hour: %w", err)
	}
	dom, err := parseField(fields[2], 1, 31, nil)
	if err != nil {
		return nil, fmt.Errorf("cron day-of-month: %w", err)
	}
	mo, err := parseField(fields[3], 1, 12, monthNames)
	if err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	dow, err := parseField(fields[4], 0, 6, dayNames)
	if err != nil {
		return nil, fmt.Errorf("cron day-of-week: %w", err)
	}
	return &Schedule{
		isCron: true,
		minute: mn, hour: hr, dom: dom, month: mo, dow: dow,
	}, nil
}

// parseField turns a single cron field into a bitmask.
// names is an optional map of textual aliases (e.g. "mon"->1).
func parseField(field string, lo, hi int, names map[string]int) (uint64, error) {
	if field == "*" || field == "?" {
		return fullMask(lo, hi), nil
	}
	var mask uint64
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("invalid step in %q", part)
			}
			step = n
			part = part[:i]
		}
		var rangeLo, rangeHi int
		if part == "*" || part == "" {
			rangeLo, rangeHi = lo, hi
		} else if i := strings.Index(part, "-"); i >= 0 {
			a, err := lookupOrInt(part[:i], names)
			if err != nil {
				return 0, err
			}
			b, err := lookupOrInt(part[i+1:], names)
			if err != nil {
				return 0, err
			}
			rangeLo, rangeHi = a, b
		} else {
			a, err := lookupOrInt(part, names)
			if err != nil {
				return 0, err
			}
			rangeLo, rangeHi = a, a
		}
		if rangeLo < lo || rangeHi > hi || rangeLo > rangeHi {
			return 0, fmt.Errorf("range %d-%d out of bounds [%d,%d]", rangeLo, rangeHi, lo, hi)
		}
		for v := rangeLo; v <= rangeHi; v += step {
			mask |= 1 << uint(v)
		}
	}
	return mask, nil
}

func lookupOrInt(s string, names map[string]int) (int, error) {
	s = strings.TrimSpace(s)
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	return v, nil
}

func fullMask(lo, hi int) uint64 {
	var m uint64
	for v := lo; v <= hi; v++ {
		m |= 1 << uint(v)
	}
	return m
}
