package dispatch

import (
	"math"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// bucket is a token bucket: it holds up to burst tokens and refills at rate
// tokens per second. One token is one delivery.
type bucket struct {
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

// epsilon absorbs float rounding, so a bucket that has refilled exactly one
// token isn't seen as 0.9999999 of one.
const epsilon = 1e-9

func (b *bucket) configure(rate float64, burst int) {
	b.rate, b.burst = rate, float64(max(burst, 1))
	b.tokens = min(b.tokens, b.burst)
}

func (b *bucket) refill(now time.Time) {
	switch {
	case b.last.IsZero():
		b.tokens = b.burst
	case now.After(b.last):
		b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	default:
		return
	}
	b.last = now
}

// take removes up to n whole tokens and returns how many it removed.
func (b *bucket) take(now time.Time, n int) int {
	b.refill(now)
	k := min(n, int(b.tokens+epsilon))
	b.tokens -= float64(k)
	return k
}

// put returns unused tokens.
func (b *bucket) put(n int) {
	b.tokens = min(b.burst, b.tokens+float64(n))
}

// wait returns how long until the bucket holds n tokens (at most a full
// burst).
func (b *bucket) wait(now time.Time, n int) time.Duration {
	b.refill(now)
	missing := min(float64(n), b.burst) - b.tokens
	if missing <= epsilon {
		return 0
	}
	return time.Duration(math.Ceil(missing / b.rate * float64(time.Second)))
}

func (b *bucket) full(now time.Time) bool {
	b.refill(now)
	return b.tokens+epsilon >= b.burst
}

// burst is how many deliveries a rate-limited queue may start at once after
// an idle spell: a tenth of a second's worth, at least 1. That spreads
// deliveries out evenly while letting fast queues start them in batches.
func burst(rate float64) int { return max(1, int(math.Ceil(rate/10))) }

type queueKey struct {
	ws   workspace.ID
	name string
}

// queueState is the dispatcher's in-memory view of a queue's limits.
type queueState struct {
	bucket   bucket
	inFlight int
}

// reserve takes up to n delivery slots from the queue's concurrency and
// rate limits. If it returns 0 because of the rate limit, wait is how long
// until a delivery may start. When it returns 0 because of the concurrency
// limit, a finishing delivery wakes the dispatcher.
func (d *Dispatcher) reserve(q store.Queue, n int, now time.Time) (got int, wait time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.queueState(q)
	if q.MaxConcurrency > 0 {
		n = min(n, q.MaxConcurrency-st.inFlight)
	}
	if n <= 0 {
		return 0, 0
	}
	if q.RatePerSecond > 0 {
		if n = st.bucket.take(now, n); n == 0 {
			return 0, d.refillWait(st, now)
		}
	}
	st.inFlight += n
	return n, 0
}

// unreserve returns slots reserved but not used: n unused tokens and slots.
func (d *Dispatcher) unreserve(q store.Queue, n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.queueState(q)
	st.inFlight -= n
	if q.RatePerSecond > 0 {
		st.bucket.put(n)
	}
}

// tokenWait returns how long until a rate-limited queue is worth another
// claim, or 0 if it has tokens now or no rate limit.
func (d *Dispatcher) tokenWait(q store.Queue, now time.Time) time.Duration {
	if q.RatePerSecond <= 0 {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.refillWait(d.queueState(q), now)
}

// refillWait waits for half a burst rather than a single token, so a fast
// rate-limited queue is claimed in batches instead of one task per round.
func (d *Dispatcher) refillWait(st *queueState, now time.Time) time.Duration {
	return st.bucket.wait(now, max(1, int(st.bucket.burst+1)/2))
}

// released is called when a delivery finishes.
func (d *Dispatcher) released(q store.Queue) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queueState(q).inFlight--
}

// queueState returns the queue's state with its current limits. d.mu must be
// held.
func (d *Dispatcher) queueState(q store.Queue) *queueState {
	k := queueKey{q.WorkspaceID, q.Name}
	st, ok := d.queues[k]
	if !ok {
		st = &queueState{}
		d.queues[k] = st
	}
	if q.RatePerSecond > 0 {
		st.bucket.configure(q.RatePerSecond, burst(q.RatePerSecond))
	}
	return st
}

// pruneQueues forgets queues with nothing in flight and a full bucket: a new
// state would behave the same.
func (d *Dispatcher) pruneQueues(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, st := range d.queues {
		if st.inFlight == 0 && (st.bucket.rate == 0 || st.bucket.full(now)) {
			delete(d.queues, k)
		}
	}
}
