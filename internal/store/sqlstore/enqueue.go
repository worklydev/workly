package sqlstore

import (
	"context"
	"sync"

	"github.com/worklydev/workly/internal/store"
	"github.com/worklydev/workly/internal/workspace"
)

// maxEnqueueWrites is how many enqueue statements run at once on Postgres.
// An enqueue that arrives while all of them are busy waits for the next one,
// with the other enqueues that arrived meanwhile. SQLite has one connection,
// so it runs one at a time: more would only queue for it, in front of the
// dispatcher.
const maxEnqueueWrites = 4

// enqueuer shares statements between concurrent enqueues, as the dispatcher's
// result writer does for attempt results. With few enqueues each is written
// at once, on its own; with many, one statement and one commit carry dozens,
// where one each would queue for connections and for Postgres's write-ahead
// log. There is no timer: an enqueue never waits for others to arrive.
//
// Enqueues share a statement only with others in the same workspace. Each
// still gets its own outcome: a task the shared statement doesn't insert, or
// any task of one that fails, is written again on its own (createTask), so
// its error is its own.
type enqueuer struct {
	store *Store

	mu      sync.Mutex
	writing int
	pending []*enqueueReq
}

type enqueueReq struct {
	ws         workspace.ID
	task       store.NewTask
	autoCreate bool
	reply      chan enqueueResult
}

type enqueueResult struct {
	task    store.Task
	created bool
	err     error
}

func (e *enqueuer) create(ctx context.Context, ws workspace.ID, nt store.NewTask, autoCreate bool) (store.Task, bool, error) {
	req := &enqueueReq{ws: ws, task: nt, autoCreate: autoCreate, reply: make(chan enqueueResult, 1)}
	e.mu.Lock()
	e.pending = append(e.pending, req)
	write := e.writing < e.maxWrites()
	if write {
		e.writing++
	}
	e.mu.Unlock()

	if write {
		// Not on this request's goroutine: the writer goes on to write for
		// enqueues that arrive meanwhile, which would hold this request's
		// response for as long as they keep coming. And this request going
		// away must not cancel theirs.
		go e.write(context.WithoutCancel(ctx))
	}
	select {
	case r := <-req.reply:
		return r.task, r.created, r.err
	case <-ctx.Done():
		// The task may still be enqueued, as when a client disconnects
		// after its enqueue commits.
		return store.Task{}, false, ctx.Err()
	}
}

func (e *enqueuer) maxWrites() int {
	if e.store.opts.Postgres {
		return maxEnqueueWrites
	}
	return 1
}

// write writes what is pending until nothing is, then gives its place up.
func (e *enqueuer) write(ctx context.Context) {
	for {
		e.mu.Lock()
		batch := e.pending
		if len(batch) > maxEnqueueBatch {
			batch = batch[:maxEnqueueBatch]
		}
		e.pending = e.pending[len(batch):]
		if len(batch) == 0 {
			e.writing--
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		e.writeBatch(ctx, batch)
	}
}

// maxEnqueueBatch caps how many enqueues one write takes.
const maxEnqueueBatch = 500

func (e *enqueuer) writeBatch(ctx context.Context, batch []*enqueueReq) {
	s := e.store
	type key struct {
		ws         workspace.ID
		autoCreate bool
	}
	var order []key
	byKey := map[key][]*enqueueReq{}
	for _, r := range batch {
		k := key{r.ws, r.autoCreate}
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], r)
	}
	anyCreated := false
	for _, k := range order {
		reqs := byKey[k]
		results := make([]enqueueResult, len(reqs))
		if len(reqs) == 1 {
			r := &results[0]
			r.task, r.created, r.err = s.createTask(ctx, s.db, k.ws, reqs[0].task, k.autoCreate)
		} else {
			nts := make([]store.NewTask, len(reqs))
			for i, r := range reqs {
				nts[i] = r.task
			}
			// One statement, so no transaction: a task it inserts is
			// committed whatever happens to the others.
			tasks, created, err := s.bulkInsert(ctx, s.db, k.ws, nts)
			for i := range reqs {
				r := &results[i]
				if err == nil && created[i] {
					r.task, r.created = tasks[i], true
					continue
				}
				r.task, r.created, r.err = s.createTask(ctx, s.db, k.ws, nts[i], k.autoCreate)
			}
		}
		for i, r := range reqs {
			anyCreated = anyCreated || results[i].created
			r.reply <- results[i]
		}
	}
	if anyCreated {
		s.wake()
	}
}
