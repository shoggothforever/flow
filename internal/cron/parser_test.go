package cron

import (
	"testing"
	"time"
)

func TestParseEvery(t *testing.T) {
	cases := []struct {
		spec string
		ok   bool
	}{
		{"every 30s", true},
		{"every 5m", true},
		{"every 2h", true},
		{"every 0s", false},
		{"every", false},
		{"every foo", false},
	}
	for _, c := range cases {
		_, err := Parse(c.spec)
		if (err == nil) != c.ok {
			t.Errorf("Parse(%q) err=%v want_ok=%v", c.spec, err, c.ok)
		}
	}
}

func TestParseDaily(t *testing.T) {
	s, err := Parse("daily 09:30")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 5, 28, 8, 0, 0, 0, time.Local)
	next := s.Next(from)
	if next.Hour() != 9 || next.Minute() != 30 {
		t.Fatalf("daily: next=%v want HH:MM=09:30", next)
	}
}

func TestParseWeekdays(t *testing.T) {
	s, err := Parse("weekdays 18:00")
	if err != nil {
		t.Fatal(err)
	}
	// Saturday
	sat := time.Date(2026, 5, 30, 12, 0, 0, 0, time.Local)
	next := s.Next(sat)
	if next.Weekday() != time.Monday {
		t.Fatalf("weekdays: from Sat next=%v (weekday=%v) want Mon", next, next.Weekday())
	}
}

func TestParseMonNamed(t *testing.T) {
	s, err := Parse("mon 09:00")
	if err != nil {
		t.Fatal(err)
	}
	thu := time.Date(2026, 5, 28, 8, 0, 0, 0, time.Local) // Thursday
	next := s.Next(thu)
	if next.Weekday() != time.Monday {
		t.Fatalf("mon 09:00: next weekday=%v want Mon", next.Weekday())
	}
}

func TestParseCron(t *testing.T) {
	cases := []string{
		"0 */2 * * *",
		"0 9 * * 1-5",
		"30 9 * * mon",
		"15 14 1 * *",
		"@hourly",
		"@daily",
		"@weekly",
	}
	for _, c := range cases {
		if _, err := Parse(c); err != nil {
			t.Errorf("Parse(%q) err: %v", c, err)
		}
	}
}

func TestCronNext(t *testing.T) {
	s, _ := Parse("0 9 * * *") // 09:00 daily
	from := time.Date(2026, 5, 28, 8, 30, 0, 0, time.Local)
	next := s.Next(from)
	if next.Hour() != 9 || next.Minute() != 0 || next.Day() != 28 {
		t.Fatalf("0 9 * * *: next=%v want today 09:00", next)
	}
	from = time.Date(2026, 5, 28, 10, 0, 0, 0, time.Local)
	next = s.Next(from)
	if next.Hour() != 9 || next.Minute() != 0 || next.Day() != 29 {
		t.Fatalf("0 9 * * * after 10am: next=%v want tomorrow 09:00", next)
	}
}

func TestCronStep(t *testing.T) {
	s, _ := Parse("*/15 * * * *") // every 15 min
	from := time.Date(2026, 5, 28, 12, 5, 0, 0, time.Local)
	next := s.Next(from)
	if next.Minute() != 15 {
		t.Fatalf("*/15 from 12:05 → %v want 12:15", next)
	}
}

func TestCronInvalid(t *testing.T) {
	_, err := Parse("60 * * * *")
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
	_, err = Parse("a b c d e")
	if err == nil {
		t.Fatal("expected parse error")
	}
}
