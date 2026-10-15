package cron

import (
	"strings"
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		expr, want string
	}{
		{"", "expected 5 fields"},
		{"* * * *", "expected 5 fields"},
		{"* * * * * *", "expected 5 fields"},
		{"@every 5m", "unknown macro"},
		{"60 * * * *", "out of range"},
		{"* 24 * * *", "out of range"},
		{"* * 0 * *", "out of range"},
		{"* * * 13 *", "out of range"},
		{"* * * * 8", "out of range"},
		{"*/0 * * * *", "invalid step"},
		{"5-1 * * * *", "runs backwards"},
		{"x * * * *", "invalid value"},
		{"0 0 30 feb *", "never matches"},
		{"0 0 31 4,6,9,11 *", "never matches"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			_, err := Parse(tt.expr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse(%q) = %v, want error containing %q", tt.expr, err, tt.want)
			}
		})
	}
}

func TestNext(t *testing.T) {
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v.UTC()
	}
	tests := []struct {
		expr, tz, after string
		want            []string
	}{
		{"* * * * *", "UTC", "2026-01-01T10:00:30Z",
			[]string{"2026-01-01T10:01:00Z", "2026-01-01T10:02:00Z"}},
		{"*/15 * * * *", "UTC", "2026-01-01T10:00:00Z",
			[]string{"2026-01-01T10:15:00Z", "2026-01-01T10:30:00Z", "2026-01-01T10:45:00Z", "2026-01-01T11:00:00Z"}},
		{"0 3 * * *", "UTC", "2026-01-01T03:00:00Z",
			[]string{"2026-01-02T03:00:00Z"}},
		{"@hourly", "UTC", "2026-01-01T10:59:59Z",
			[]string{"2026-01-01T11:00:00Z"}},
		{"0 9 * * mon-fri", "UTC", "2026-01-02T09:00:00Z", // a Friday
			[]string{"2026-01-05T09:00:00Z", "2026-01-06T09:00:00Z"}},
		{"0 0 * * 7", "UTC", "2026-01-01T00:00:00Z", // 7 is Sunday
			[]string{"2026-01-04T00:00:00Z"}},
		{"30 2 1-7/3 jan,MAR *", "UTC", "2026-01-01T00:00:00Z",
			[]string{"2026-01-01T02:30:00Z", "2026-01-04T02:30:00Z", "2026-01-07T02:30:00Z", "2026-03-01T02:30:00Z"}},
		{"0 0 5/10 * *", "UTC", "2026-01-01T00:00:00Z",
			[]string{"2026-01-05T00:00:00Z", "2026-01-15T00:00:00Z", "2026-01-25T00:00:00Z", "2026-02-05T00:00:00Z"}},
		{"0 0 29 2 *", "UTC", "2026-01-01T00:00:00Z",
			[]string{"2028-02-29T00:00:00Z", "2032-02-29T00:00:00Z"}},
		// Both day fields restricted: the 13th, or any Friday.
		{"0 0 13 * fri", "UTC", "2026-02-01T00:00:00Z",
			[]string{"2026-02-06T00:00:00Z", "2026-02-13T00:00:00Z", "2026-02-20T00:00:00Z"}},
		// One starts with *: both must match (Friday the 13th, every other day).
		{"0 0 */2 * fri", "UTC", "2026-02-01T00:00:00Z",
			[]string{"2026-02-13T00:00:00Z", "2026-02-27T00:00:00Z"}},
		{"0 3 * * *", "Europe/Stockholm", "2026-01-01T00:00:00Z",
			[]string{"2026-01-01T02:00:00Z", "2026-01-02T02:00:00Z"}},
		// Spring forward (Stockholm, 2026-03-29 02:00 → 03:00): 02:30 doesn't
		// exist and runs at 03:00 local, once.
		{"30 2 * * *", "Europe/Stockholm", "2026-03-28T12:00:00Z",
			[]string{"2026-03-29T01:00:00Z", "2026-03-30T00:30:00Z"}},
		{"*/30 * * * *", "Europe/Stockholm", "2026-03-29T00:15:00Z",
			[]string{"2026-03-29T00:30:00Z", "2026-03-29T01:00:00Z", "2026-03-29T01:30:00Z"}},
		// Fall back (Stockholm, 2026-10-25 03:00 → 02:00): 02:30 happens
		// twice and runs once, at its first occurrence.
		{"30 2 * * *", "Europe/Stockholm", "2026-10-24T12:00:00Z",
			[]string{"2026-10-25T00:30:00Z", "2026-10-26T01:30:00Z"}},
		{"0 * * * *", "Europe/Stockholm", "2026-10-24T23:30:00Z",
			[]string{"2026-10-25T00:00:00Z", "2026-10-25T02:00:00Z", "2026-10-25T03:00:00Z"}},
		// A half-hour shift (Lord Howe, 2026-04-05 02:00 → 01:30).
		{"45 1 * * *", "Australia/Lord_Howe", "2026-04-04T00:00:00Z",
			[]string{"2026-04-04T14:45:00Z", "2026-04-05T15:15:00Z"}},
	}
	for _, tt := range tests {
		t.Run(tt.expr+" "+tt.tz+" "+tt.after, func(t *testing.T) {
			s, err := Parse(tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			loc := mustLoad(t, tt.tz)
			cur := at(tt.after)
			for i, w := range tt.want {
				cur = s.Next(cur, loc)
				if !cur.Equal(at(w)) {
					t.Fatalf("next #%d = %s, want %s", i+1, cur.Format(time.RFC3339), w)
				}
			}
		})
	}
}

func TestPrev(t *testing.T) {
	loc := mustLoad(t, "Europe/Stockholm")
	tests := []struct {
		expr string
		at   time.Time
		want time.Time
	}{
		{"* * * * *", time.Date(2026, 5, 1, 10, 0, 30, 0, time.UTC), time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)},
		{"* * * * *", time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC), time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)},
		{"0 3 * * *", time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC), time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)},
		{"0 3 * * *", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 30, 1, 0, 0, 0, time.UTC)},
		{"@yearly", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC)},
		{"0 0 29 2 *", time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2028, 2, 28, 23, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		s, err := Parse(tt.expr)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Prev(tt.at, loc); !got.Equal(tt.want) {
			t.Errorf("%q Prev(%s) = %s, want %s", tt.expr, tt.at, got, tt.want)
		}
	}
}

func TestLoadLocation(t *testing.T) {
	for _, name := range []string{"", "Local", "Mars/Olympus"} {
		if _, err := LoadLocation(name); err == nil {
			t.Errorf("LoadLocation(%q) succeeded", name)
		}
	}
	if _, err := LoadLocation("America/New_York"); err != nil {
		t.Error(err)
	}
}
