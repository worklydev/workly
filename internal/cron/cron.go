// Package cron parses cron expressions and finds the times they match in a
// timezone.
//
// Expressions have five fields (minute, hour, day of month, month, day of
// week) with *, lists, ranges, steps and three-letter names, or one of the
// macros @yearly, @annually, @monthly, @weekly, @daily, @midnight and
// @hourly. As in Vixie cron, when both day fields are restricted (neither
// starts with *), a day matches if either matches.
//
// Times are matched on the wall clock of the schedule's timezone. Across a
// daylight saving change, a time in the skipped hour fires at the first
// instant after the gap, and a time in the repeated hour fires once, at its
// first occurrence.
package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // a fallback where the system has no zoneinfo (distroless, Windows)
)

// Schedule is a parsed cron expression.
type Schedule struct {
	minute, hour, dom, month, dow uint64
	// domStar and dowStar record whether the day fields start with *, which
	// decides how they combine.
	domStar, dowStar bool
}

type field struct {
	name     string
	min, max int
	names    []string // names[i] is the name of min+i
}

var (
	minuteField = field{name: "minute", min: 0, max: 59}
	hourField   = field{name: "hour", min: 0, max: 23}
	domField    = field{name: "day of month", min: 1, max: 31}
	monthField  = field{name: "month", min: 1, max: 12,
		names: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}}
	// Day of week accepts 0-7, where 0 and 7 are both Sunday.
	dowField = field{name: "day of week", min: 0, max: 7,
		names: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}}
)

var macros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// Parse parses a cron expression. It rejects expressions that never match,
// such as February 30th.
func Parse(expr string) (Schedule, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "@") {
		m, ok := macros[strings.ToLower(expr)]
		if !ok {
			return Schedule{}, fmt.Errorf("unknown macro %q: use @yearly, @monthly, @weekly, @daily or @hourly", expr)
		}
		expr = m
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("expected 5 fields (minute hour day-of-month month day-of-week), got %d", len(fields))
	}
	var (
		s   Schedule
		err error
	)
	for i, p := range []struct {
		f   field
		dst *uint64
	}{{minuteField, &s.minute}, {hourField, &s.hour}, {domField, &s.dom}, {monthField, &s.month}, {dowField, &s.dow}} {
		if *p.dst, err = parseField(fields[i], p.f); err != nil {
			return Schedule{}, err
		}
	}
	// Sunday is both 0 and 7.
	if s.dow&(1<<7) != 0 {
		s.dow = s.dow&^(1<<7) | 1
	}
	s.domStar = strings.HasPrefix(fields[2], "*")
	s.dowStar = strings.HasPrefix(fields[4], "*")
	if s.Next(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC).IsZero() {
		return Schedule{}, errors.New("the expression never matches a date")
	}
	return s, nil
}

func parseField(s string, f field) (uint64, error) {
	var bits uint64
	for item := range strings.SplitSeq(s, ",") {
		b, err := parseItem(item, f)
		if err != nil {
			return 0, fmt.Errorf("%s field %q: %w", f.name, s, err)
		}
		bits |= b
	}
	return bits, nil
}

// parseItem parses *, a, a-b, */n, a-b/n or a/n (a to the maximum).
func parseItem(item string, f field) (uint64, error) {
	rng, stepText, hasStep := strings.Cut(item, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("invalid step %q", stepText)
		}
		step = n
	}
	var lo, hi int
	switch {
	case rng == "*":
		lo, hi = f.min, f.max
		if f.max == 7 {
			hi = 6 // * is Sunday to Saturday once
		}
	case strings.Contains(rng, "-"):
		a, b, _ := strings.Cut(rng, "-")
		var err error
		if lo, err = f.value(a); err != nil {
			return 0, err
		}
		if hi, err = f.value(b); err != nil {
			return 0, err
		}
		if lo > hi {
			return 0, fmt.Errorf("range %q runs backwards", rng)
		}
	default:
		v, err := f.value(rng)
		if err != nil {
			return 0, err
		}
		lo, hi = v, v
		if hasStep {
			hi = f.max
		}
	}
	var bits uint64
	for v := lo; v <= hi; v += step {
		bits |= 1 << v
	}
	return bits, nil
}

func (f field) value(s string) (int, error) {
	for i, name := range f.names {
		if strings.EqualFold(s, name) {
			return f.min + i, nil
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	if n < f.min || n > f.max {
		return 0, fmt.Errorf("%d is out of range (%d-%d)", n, f.min, f.max)
	}
	return n, nil
}

func has(bits uint64, v int) bool { return bits&(1<<v) != 0 }

func (s Schedule) dayMatches(t time.Time) bool {
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// searchDays bounds the search for the next match. Eight years always
// include a February 29th.
const searchDays = 8 * 366

// Next returns the first time after the given one that the schedule
// matches in loc, or the zero time if there is none within eight years.
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time {
	start := after.In(loc)
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	for i := range searchDays {
		d := day.AddDate(0, 0, i)
		if !has(s.month, int(d.Month())) || !s.dayMatches(d) {
			continue
		}
		for h := range 24 {
			if !has(s.hour, h) || i == 0 && h < start.Hour() {
				continue
			}
			for m := range 60 {
				if !has(s.minute, m) || i == 0 && h == start.Hour() && m < start.Minute() {
					continue
				}
				if t := resolve(d.Year(), d.Month(), d.Day(), h, m, loc); t.After(after) {
					return t
				}
			}
		}
	}
	return time.Time{}
}

// Prev returns the last time at or before the given one that the schedule
// matches in loc, or the zero time if there is none within eight years.
func (s Schedule) Prev(at time.Time, loc *time.Location) time.Time {
	// Search backwards in growing windows, so a frequent schedule needs a
	// few steps and a yearly one still finds its slot.
	for _, w := range []time.Duration{time.Minute, time.Hour, 24 * time.Hour, 32 * 24 * time.Hour, 366 * 24 * time.Hour, searchDays * 24 * time.Hour} {
		t := s.Next(at.Add(-w), loc)
		if t.IsZero() || t.After(at) {
			continue
		}
		for {
			n := s.Next(t, loc)
			if n.IsZero() || n.After(at) {
				return t
			}
			t = n
		}
	}
	return time.Time{}
}

// resolve returns the instant a wall-clock time fires in loc: its first
// occurrence if the hour repeats, or the end of the gap if it was skipped.
func resolve(year int, month time.Month, day, hour, min int, loc *time.Location) time.Time {
	wall := time.Date(year, month, day, hour, min, 0, 0, time.UTC)
	t := time.Date(year, month, day, hour, min, 0, 0, loc)
	if wallClock(t, loc).Equal(wall) {
		// Daylight saving shifts are at most two hours; an earlier instant
		// with the same wall time is the first occurrence.
		first := t
		for _, back := range []time.Duration{2 * time.Hour, time.Hour, 30 * time.Minute} {
			if e := t.Add(-back); e.Before(first) && wallClock(e, loc).Equal(wall) {
				first = e
			}
		}
		return first.UTC()
	}
	// In a gap: find the first second whose wall time is past it.
	lo, hi := t.Add(-3*time.Hour).Unix(), t.Add(3*time.Hour).Unix()
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if wallClock(time.Unix(mid, 0), loc).Before(wall) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return time.Unix(hi, 0).UTC()
}

// wallClock returns t's wall-clock time in loc, as a UTC time for comparison.
func wallClock(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, time.UTC)
}

// LoadLocation loads an IANA timezone. Unlike time.LoadLocation it rejects
// "" and "Local", whose meaning depends on the server.
func LoadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("unknown timezone %q: use an IANA name such as UTC or Europe/Stockholm", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: use an IANA name such as UTC or Europe/Stockholm", name)
	}
	return loc, nil
}
