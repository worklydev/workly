// Package live tells dashboards when to refresh. Whatever changes a
// workspace's queues, tasks, schedules or runs (an API call, a delivery)
// calls Hub.Changed; the hub coalesces those into change notices for every
// replica through the store, and each replica passes them on to the event
// streams (GET /v1/events) it serves for that workspace.
package live

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

const (
	// flushInterval caps how often each replica publishes notices, and so how
	// often a busy workspace's dashboards refresh.
	flushInterval = 250 * time.Millisecond
	// maxListenBackoff caps the wait before listening again after the
	// connection failed.
	maxListenBackoff = 30 * time.Second
)

type Hub struct {
	store store.Changes
	log   *slog.Logger

	mu      sync.Mutex
	changed map[workspace.ID]struct{}
	subs    map[workspace.ID]map[chan struct{}]struct{}
	kick    chan struct{}
	stopped chan struct{}
}

func New(s store.Changes, log *slog.Logger) *Hub {
	return &Hub{
		store:   s,
		log:     log,
		changed: map[workspace.ID]struct{}{},
		subs:    map[workspace.ID]map[chan struct{}]struct{}{},
		kick:    make(chan struct{}, 1),
		stopped: make(chan struct{}),
	}
}

// Changed records that the workspace changed. It doesn't block.
func (h *Hub) Changed(ws workspace.ID) {
	h.mu.Lock()
	h.changed[ws] = struct{}{}
	h.mu.Unlock()
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// Subscribe returns a channel that receives a value after the workspace
// changes. Values coalesce: one may stand for several changes. Call cancel
// when done.
func (h *Hub) Subscribe(ws workspace.ID) (c <-chan struct{}, cancel func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[ws] == nil {
		h.subs[ws] = map[chan struct{}]struct{}{}
	}
	h.subs[ws][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[ws], ch)
		if len(h.subs[ws]) == 0 {
			delete(h.subs, ws)
		}
		h.mu.Unlock()
	}
}

// Stopped is closed when Run returns, so streams can end before the server
// shuts down rather than hold it up.
func (h *Hub) Stopped() <-chan struct{} { return h.stopped }

// Run publishes and listens for notices until ctx is done.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.stopped)
	var wg sync.WaitGroup
	wg.Go(func() { h.listen(ctx) })
	h.publish(ctx)
	wg.Wait()
}

func (h *Hub) publish(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.kick:
		}
		h.mu.Lock()
		ws := make([]workspace.ID, 0, len(h.changed))
		for id := range h.changed {
			ws = append(ws, id)
		}
		clear(h.changed)
		h.mu.Unlock()
		slices.Sort(ws)
		if err := h.store.PublishChanges(ctx, ws); err != nil && ctx.Err() == nil {
			// Dashboards also poll, so a lost notice only delays them.
			h.log.Warn("publish changes", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(flushInterval):
		}
	}
}

func (h *Hub) listen(ctx context.Context) {
	backoff := time.Second
	for {
		listening := false
		err := h.store.ListenChanges(ctx, func() {
			listening = true
			backoff = time.Second
			// Notices may have been missed while not listening.
			h.notifyAll()
		}, h.notify)
		if ctx.Err() != nil {
			return
		}
		h.log.Warn("listen for changes", "err", err, "retry_in", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if !listening {
			backoff = min(2*backoff, maxListenBackoff)
		}
	}
}

func (h *Hub) notify(ws []workspace.ID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range ws {
		for ch := range h.subs[id] {
			signal(ch)
		}
	}
}

func (h *Hub) notifyAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, subs := range h.subs {
		for ch := range subs {
			signal(ch)
		}
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
