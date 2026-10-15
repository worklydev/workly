package id

import (
	"strings"
	"testing"
	"time"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		in   [16]byte
		want string
	}{
		{[16]byte{}, "00000000000000000000000000"},
		{[16]byte{15: 1}, "00000000000000000000000001"},
		{[16]byte{15: 32}, "00000000000000000000000010"},
		{[16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"},
	}
	for _, tt := range tests {
		if got := encode(tt.in); got != tt.want {
			t.Errorf("encode(%x) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestSortsByTime(t *testing.T) {
	base := time.UnixMilli(1790000000000)
	var g generator
	a, b := g.ulid(base), g.ulid(base.Add(time.Millisecond))
	if a[:10] >= b[:10] {
		t.Fatalf("timestamp prefix not increasing: %s, %s", a, b)
	}
	if got := New("task"); !strings.HasPrefix(got, "task_") || len(got) != len("task_")+26 {
		t.Fatalf("New = %q", got)
	}
}

func TestMonotonic(t *testing.T) {
	var g generator
	base := time.UnixMilli(1790000000000)
	// The same millisecond, then a clock that went back.
	times := []time.Time{base, base, base, base.Add(-time.Second), base.Add(time.Millisecond)}
	prev := ""
	for i, at := range times {
		got := g.ulid(at)
		if got <= prev {
			t.Fatalf("ulid #%d = %s, not after %s", i, got, prev)
		}
		prev = got
	}
}
