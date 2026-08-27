package runner

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Phases is what the hosts are busy with right now, per agent: fetching an
// image, laying out a home, writing one back.
//
// It is deliberately memory only. This is live data — what is happening at this
// second — and after a restart of the control plane nothing is happening any
// more that this could still be about. The recording holds the durable half:
// there every phase stands with its figures, afterwards and for good.
//
// It exists because "the agent is starting" was the whole answer the platform
// could give for a wait that runs to three quarters of an hour on a fresh host.
// The figures were there all along; nothing kept them where somebody could ask.
type Phases struct {
	mu  sync.Mutex
	cur map[uuid.UUID]Phase
}

// Phase is one running step, in the terms of that step: an image counts bytes,
// a home counts files. Total 0 means the step does not know its own end — a
// scan finds out how much there is by walking it.
type Phase struct {
	Phase      string    `json:"phase"`
	Detail     string    `json:"detail,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
	BytesTotal int64     `json:"bytes_total,omitempty"`
	Count      int64     `json:"count,omitempty"`
	CountTotal int64     `json:"count_total,omitempty"`
	Since      time.Time `json:"since"`
	Updated    time.Time `json:"updated"`
	// Runner is the host doing it. With one machine that is not a question;
	// with a second one it is the first one.
	Runner uuid.UUID `json:"runner,omitempty"`
}

// phaseStale: how long a phase without a sign of life still counts as running.
// A running step reports every progressEvery, so anything older than this
// belongs to a host that has gone away mid-step — and a progress bar that
// stands still for ten minutes is worse than none, because it claims something.
const phaseStale = 3 * progressEvery

func NewPhases() *Phases { return &Phases{cur: map[uuid.UUID]Phase{}} }

// Note takes what a host reported. The last report of a step (Done) ends it:
// from there the figures belong to the recording, not to a progress bar.
func (p *Phases) Note(runnerID uuid.UUID, pr Progress) {
	if p == nil || pr.AgentID == uuid.Nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if pr.Done {
		delete(p.cur, pr.AgentID)
		return
	}
	now := time.Now()
	seit := now
	// A step keeps the start it had. Its duration is what somebody reads first,
	// and restarting it at every sign of life would make every wait look fresh.
	if alt, ok := p.cur[pr.AgentID]; ok && alt.Phase == pr.Phase {
		seit = alt.Since
	}
	p.cur[pr.AgentID] = Phase{
		Phase: pr.Phase, Detail: pr.Detail,
		Bytes: pr.Bytes, BytesTotal: pr.BytesTotal,
		Count: pr.Count, CountTotal: pr.CountTotal,
		Since: seit, Updated: now, Runner: runnerID,
	}
}

// Clear ends whatever was running for this agent — the caller knows the step is
// over because the call it was part of has returned.
func (p *Phases) Clear(agentID uuid.UUID) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.cur, agentID)
	p.mu.Unlock()
}

// Of: what is this agent waiting on at the moment, if anything.
func (p *Phases) Of(agentID uuid.UUID) (Phase, bool) {
	if p == nil {
		return Phase{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ph, ok := p.cur[agentID]
	if !ok || time.Since(ph.Updated) > phaseStale {
		return Phase{}, false
	}
	return ph, true
}

// All: the same for everybody at once, for a list that would otherwise ask per
// row. Stale entries are dropped on the way — this is the only place that
// tidies up, and a map that only ever grows is a leak in a process that runs
// for months.
func (p *Phases) All() map[uuid.UUID]Phase {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[uuid.UUID]Phase, len(p.cur))
	for id, ph := range p.cur {
		if time.Since(ph.Updated) > phaseStale {
			delete(p.cur, id)
			continue
		}
		out[id] = ph
	}
	return out
}
