package dispatch

import (
	"context"
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

// workspaceState is the dispatcher's view of a workspace's own cap on
// concurrent deliveries.
type workspaceState struct {
	inFlight int
	// limit is the workspace's cap from Options.WorkspaceMaxInFlight, 0 for
	// none, as of checked.
	limit   int
	checked time.Time
}

// limitTTL is how long a workspace's cap is used before it is looked up
// again, so a hosted service's lookup isn't on every dispatch round.
const limitTTL = time.Minute

// room is how many more deliveries the workspace's cap allows. d.mu must be
// held.
func (w *workspaceState) room() int {
	if w.limit <= 0 {
		return math.MaxInt
	}
	return max(0, w.limit-w.inFlight)
}

// workspaceState returns ws's state. d.mu must be held.
func (d *Dispatcher) workspaceState(ws workspace.ID) *workspaceState {
	st, ok := d.workspaces[ws]
	if !ok {
		st = &workspaceState{}
		d.workspaces[ws] = st
	}
	return st
}

// refreshLimits looks up the caps of the queues' workspaces that are new or
// older than limitTTL. A failed lookup keeps the previous cap until the next
// try, rather than stopping the workspace's deliveries.
func (d *Dispatcher) refreshLimits(ctx context.Context, queues []store.Queue, now time.Time) {
	lookup := d.opts.WorkspaceMaxInFlight
	if lookup == nil {
		return
	}
	var stale []workspace.ID
	d.mu.Lock()
	for _, q := range queues {
		st := d.workspaceState(q.WorkspaceID)
		if now.Sub(st.checked) >= limitTTL {
			st.checked = now // once per round, however many queues it has
			stale = append(stale, q.WorkspaceID)
		}
	}
	d.mu.Unlock()
	for _, ws := range stale {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		limit, err := lookup(lctx, ws)
		cancel()
		if err != nil {
			d.log.Warn("could not look up a workspace's delivery limit", "workspace", ws, "err", err)
			continue
		}
		d.mu.Lock()
		d.workspaceState(ws).limit = max(0, limit)
		d.mu.Unlock()
	}
}

// workspaceRoom is how many more deliveries ws's cap allows.
func (d *Dispatcher) workspaceRoom(ws workspace.ID) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.workspaceState(ws).room()
}

// reserve takes up to n delivery slots from the queue's concurrency and
// rate limits and its workspace's cap. If it returns 0 because of the rate
// limit, wait is how long until a delivery may start. When it returns 0
// because of a concurrency limit, a finishing delivery wakes the dispatcher.
func (d *Dispatcher) reserve(q store.Queue, n int, now time.Time) (got int, wait time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st, ws := d.queueState(q), d.workspaceState(q.WorkspaceID)
	if q.MaxConcurrency > 0 {
		n = min(n, q.MaxConcurrency-st.inFlight)
	}
	n = min(n, ws.room())
	if n <= 0 {
		return 0, 0
	}
	if q.RatePerSecond > 0 {
		if n = st.bucket.take(now, n); n == 0 {
			return 0, d.refillWait(st, now)
		}
	}
	st.inFlight += n
	ws.inFlight += n
	return n, 0
}

// headroom is how many deliveries the queue's limits and its workspace's
// cap let it start now, without taking them. If that is 0 because of the
// rate limit, wait is how long until one may start.
func (d *Dispatcher) headroom(q store.Queue, now time.Time) (n int, wait time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.queueState(q)
	n = d.workspaceState(q.WorkspaceID).room()
	if q.MaxConcurrency > 0 {
		n = min(n, q.MaxConcurrency-st.inFlight)
	}
	if n <= 0 {
		return 0, 0
	}
	if q.RatePerSecond > 0 {
		st.bucket.refill(now)
		if n = min(n, int(st.bucket.tokens+epsilon)); n == 0 {
			return 0, d.refillWait(st, now)
		}
	}
	return n, 0
}

// allotSlots shares free slots among queues that have room for room[i]
// deliveries each: equal shares, and what a queue has no room for goes to
// the others. When there are fewer slots than queues, the first ones get
// them.
func allotSlots(room []int, free int) []int {
	allot := make([]int, len(room))
	for free > 0 {
		open := 0
		for i, r := range room {
			if allot[i] < r {
				open++
			}
		}
		if open == 0 {
			break
		}
		share := max(1, free/open)
		for i, r := range room {
			k := min(share, r-allot[i], free)
			allot[i] += k
			free -= k
		}
	}
	return allot
}

// allotByWorkspace shares free slots equally among workspaces, and each
// workspace's share among its queues, so a workspace's share doesn't grow
// with how many queues it has. groups[g] lists the indexes in room of
// workspace g's queues, and caps[g] is how many more deliveries its own cap
// allows.
func allotByWorkspace(groups [][]int, caps, room []int, free int) []int {
	wsRoom := make([]int, len(groups))
	for g, qs := range groups {
		sum := 0
		for _, i := range qs {
			sum += room[i]
		}
		wsRoom[g] = min(caps[g], sum)
	}
	allot := make([]int, len(room))
	for g, share := range allotSlots(wsRoom, free) {
		qRoom := make([]int, len(groups[g]))
		for j, i := range groups[g] {
			qRoom[j] = room[i]
		}
		for j, n := range allotSlots(qRoom, share) {
			allot[groups[g][j]] = n
		}
	}
	return allot
}

// unreserve returns slots reserved but not used: n unused tokens and slots.
func (d *Dispatcher) unreserve(q store.Queue, n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.queueState(q)
	st.inFlight -= n
	d.workspaceState(q.WorkspaceID).inFlight -= n
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
	d.workspaceState(q.WorkspaceID).inFlight--
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

// pruneQueues forgets queues with nothing in flight and a full bucket, and
// workspaces with nothing in flight whose cap is due a lookup: a new state
// would behave the same.
func (d *Dispatcher) pruneQueues(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, st := range d.queues {
		if st.inFlight == 0 && (st.bucket.rate == 0 || st.bucket.full(now)) {
			delete(d.queues, k)
		}
	}
	for ws, st := range d.workspaces {
		if st.inFlight == 0 && now.Sub(st.checked) >= limitTTL {
			delete(d.workspaces, ws)
		}
	}
}
