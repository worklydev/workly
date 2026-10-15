package dispatch

import (
	"testing"
	"time"
)

func TestBucket(t *testing.T) {
	var b bucket
	b.configure(5, 2)
	start := time.Unix(1790000000, 0)

	// A new bucket is full.
	if n := b.take(start, 10); n != 2 {
		t.Fatalf("take from a new bucket = %d, want the burst of 2", n)
	}
	if n := b.take(start, 1); n != 0 {
		t.Fatalf("take from an empty bucket = %d, want 0", n)
	}
	if w := b.wait(start, 1); w != 200*time.Millisecond {
		t.Fatalf("wait for 1 token = %v, want 200ms at 5/s", w)
	}
	if w := b.wait(start, 5); w != 400*time.Millisecond {
		t.Fatalf("wait for 5 tokens = %v, want 400ms (capped at the burst)", w)
	}

	// One token every 200ms, however it's polled.
	at := start.Add(199 * time.Millisecond)
	if n := b.take(at, 1); n != 0 {
		t.Fatalf("take after 199ms = %d, want 0", n)
	}
	at = start.Add(200 * time.Millisecond)
	if n := b.take(at, 5); n != 1 {
		t.Fatalf("take after 200ms = %d, want 1", n)
	}

	// Refill stops at the burst; unused tokens can be returned.
	at = at.Add(time.Hour)
	if !b.full(at) {
		t.Fatal("bucket not full after an hour")
	}
	if n := b.take(at, 2); n != 2 {
		t.Fatalf("take = %d, want 2", n)
	}
	b.put(1)
	if n := b.take(at, 5); n != 1 {
		t.Fatalf("take after returning 1 = %d, want 1", n)
	}

	// Lowering the burst caps the tokens.
	b.put(2)
	b.configure(5, 1)
	if n := b.take(at, 5); n != 1 {
		t.Fatalf("take after lowering the burst = %d, want 1", n)
	}
}

// Over a long run, a bucket admits at most rate * duration plus the burst.
// Polling late loses a little: a full bucket can't bank the overshoot.
func TestBucketRate(t *testing.T) {
	var b bucket
	b.configure(5, 1)
	start := time.Unix(1790000000, 0)
	total := 0
	for ms := 0; ms <= 60_000; ms += 7 {
		total += b.take(start.Add(time.Duration(ms)*time.Millisecond), 100)
	}
	if limit := 5*60 + 1; total > limit || total < limit*95/100 {
		t.Fatalf("took %d tokens in 60s at 5/s polling every 7ms, want at most %d and close to it", total, limit)
	}
}
