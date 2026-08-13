package runner

import (
	"context"
	"time"

	"github.com/google/uuid"

	"covey/internal/sandboxfs"
)

// The control plane's side of the runner view (spec/16, stage 5): what a
// runner is, what it can do, and what it is carrying right now.

// Live is what the pool knows about a connected runner — everything the
// database cannot say, because it is true only while the connection stands.
type Live struct {
	RunnerID  uuid.UUID `json:"runner_id"`
	Connected bool      `json:"connected"`
	Protocol  int       `json:"protocol"`
	Version   string    `json:"version,omitempty"`
	Arch      string    `json:"arch,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Images    []string  `json:"images,omitempty"`
	Sandboxes int       `json:"sandboxes"`
	// Outdated: it speaks an older protocol than this control plane. Named
	// rather than merely tolerated — version drift between separately delivered
	// parts is a thing you want to see before it becomes a symptom.
	Outdated bool `json:"outdated"`
}

// LiveFor reports what the pool knows about the connected runners of an
// organisation, keyed by runner ID.
func (p *Pool) LiveFor(orgID uuid.UUID) map[uuid.UUID]Live {
	p.mu.Lock()
	conns := make([]*conn, 0, len(p.conns))
	for _, c := range p.conns {
		if c.orgID == orgID {
			conns = append(conns, c)
		}
	}
	p.mu.Unlock()

	out := make(map[uuid.UUID]Live, len(conns))
	for _, c := range conns {
		c.mu.Lock()
		running := c.sandboxes
		c.mu.Unlock()
		out[c.runnerID] = Live{
			RunnerID: c.runnerID, Connected: true, Protocol: c.protocol,
			Version: c.version, Arch: c.arch, Tags: c.tags, Images: c.images,
			Sandboxes: running, Outdated: c.protocol < Protocol,
		}
	}
	return out
}

// Capacity asks a runner what it is carrying. Asked rather than remembered:
// free disk changes while nobody is looking, and a cached figure is the kind
// that reassures right up to the moment the disk is full.
func (p *Pool) Capacity(ctx context.Context, runnerID uuid.UUID) (CapacityReport, error) {
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()
	if c == nil {
		return CapacityReport{}, ErrNoRunner
	}
	answer, err := c.ask(ctx, TypeCapacity, nil, 30*time.Second)
	if err != nil {
		return CapacityReport{}, err
	}
	return decode[CapacityReport](answer)
}

// SyncNow forces a sync of an agent's home — "back up now", before a
// maintenance window. It goes through the same path as falling asleep, so the
// snapshot it produces is not a second kind.
func (p *Pool) SyncNow(ctx context.Context, agentID, orgID uuid.UUID, reason string) error {
	c := p.connFor(orgID, p.lastRunnerOf(ctx, agentID))
	if c == nil {
		return ErrNoRunner
	}
	// Whatever the file browser has changed and not yet carried out goes in
	// with it — two syncs in a row would only produce two snapshots of the
	// same state.
	p.flushDirtyFlag(agentID)
	return p.syncHomeReason(ctx, c, agentID, orgID, reason)
}

// Restore brings a home back to an earlier state. Only when the agent is not
// running — otherwise the running sandbox writes into a home that changes
// underneath it. That check lives in the HTTP layer, where the agent's status
// is known.
func (p *Pool) Restore(ctx context.Context, agentID, orgID uuid.UUID, snapshot string) error {
	c := p.connFor(orgID, p.lastRunnerOf(ctx, agentID))
	if c == nil {
		return ErrNoRunner
	}
	answer, err := c.ask(ctx, TypeHomeOp, HomeOp{
		AgentID: agentID, OrgID: orgID, Op: OpRestore, Snapshot: snapshot,
	}, homeOpTimeout)
	if err != nil {
		return err
	}
	res, err := decode[HomeResult](answer)
	if err != nil {
		return err
	}
	if res.Err != "" {
		return sandboxfs.ErrorFromKind(res.ErrKind, res.Err)
	}
	// The restored state becomes the current one — otherwise the next wake
	// would materialise the newer snapshot over it and the rollback would last
	// exactly until then.
	return p.syncHomeReason(ctx, c, agentID, orgID, "restore")
}

func (p *Pool) lastRunnerOf(ctx context.Context, agentID uuid.UUID) uuid.UUID {
	if p.HomeInfo == nil {
		return uuid.Nil
	}
	_, last, _, err := p.HomeInfo(ctx, agentID)
	if err != nil {
		return uuid.Nil
	}
	return last
}

// flushDirtyFlag drops a pending settling period without syncing — the caller
// is about to sync anyway.
func (p *Pool) flushDirtyFlag(agentID uuid.UUID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d := p.dirty[agentID]; d != nil {
		d.timer.Stop()
		delete(p.dirty, agentID)
	}
}
