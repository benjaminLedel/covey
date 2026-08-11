package runner

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// What the file browser writes lands in the runner's working copy — the live
// truth, and the only place it can land while the agent is asleep. But the
// snapshot in the home store is what the next wake materialises, and on another
// runner it is the ONLY thing there is. Between the two lies a window in which
// an upload can disappear without anyone having deleted it.
//
// So a change through the browser marks the home dirty and a sync follows.
// Debounced, because a fifty-file upload is fifty changes and a sync walks the
// whole home: doing it per file would turn a drag-and-drop into minutes of
// scanning. And flushed before every start, because that is the moment where
// getting it wrong actually costs something.

// dirtySettle is how long the browser has to stay quiet before the sync runs.
// Short enough that nobody has to think about it, long enough that a bulk
// upload produces one sync and not one per file.
const dirtySettle = 3 * time.Second

type dirtyHome struct {
	conn  *conn
	orgID uuid.UUID
	timer *time.Timer
}

// markHomeDirty notes a change and (re)starts the settling period.
func (p *Pool) markHomeDirty(c *conn, agentID, orgID uuid.UUID) {
	if p.SnapshotTaken == nil {
		return // no home store: the working copy is all there is
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if d := p.dirty[agentID]; d != nil {
		d.conn = c
		d.timer.Reset(dirtySettle)
		return
	}
	d := &dirtyHome{conn: c, orgID: orgID}
	d.timer = time.AfterFunc(dirtySettle, func() { p.flushHome(context.Background(), agentID) })
	p.dirty[agentID] = d
}

// flushHome syncs a home that the browser has changed, if there is one. Called
// by the timer and before every start — the second one is what makes this
// reliable rather than merely likely: whatever the settling period has not
// carried out yet is carried out here, before the snapshot is materialised over
// the working copy.
func (p *Pool) flushHome(ctx context.Context, agentID uuid.UUID) {
	p.mu.Lock()
	d := p.dirty[agentID]
	if d != nil {
		delete(p.dirty, agentID)
		d.timer.Stop()
	}
	p.mu.Unlock()
	if d == nil {
		return
	}
	p.syncHome(context.WithoutCancel(ctx), d.conn, agentID, d.orgID)
}

// FlushHomes syncs everything the file browser has changed and not yet carried
// out — for a shutdown. A settling period that is still running would fire into
// a connection that is already gone, and the change would then live only in the
// working copy: exactly the window this whole mechanism exists to close.
func (p *Pool) FlushHomes(ctx context.Context) {
	p.mu.Lock()
	pending := make([]uuid.UUID, 0, len(p.dirty))
	for id := range p.dirty {
		pending = append(pending, id)
	}
	p.mu.Unlock()
	for _, id := range pending {
		p.flushHome(ctx, id)
	}
}
