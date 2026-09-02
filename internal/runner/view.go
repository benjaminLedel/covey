package runner

import (
	"context"
	"errors"
	"fmt"
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
	// What the host says about itself, beside the effective sets above: an
	// operator who assigns nothing has to be able to see why an agent still
	// does not land there.
	// Features are what this build can do beyond the messages that have always
	// existed. The interface needs them to tell "this host has nothing to say"
	// apart from "this build cannot say anything".
	Features       []string `json:"features,omitempty"`
	ReportedTags   []string `json:"reported_tags,omitempty"`
	ReportedImages []string `json:"reported_images,omitempty"`
	Sandboxes      int      `json:"sandboxes"`
	// MaxSandboxes is what this host says it will carry at once, 0 = no limit.
	MaxSandboxes int `json:"max_sandboxes,omitempty"`
	// Outdated: it speaks an older protocol than this control plane. Named
	// rather than merely tolerated — version drift between separately delivered
	// parts is a thing you want to see before it becomes a symptom.
	Outdated bool `json:"outdated"`
	// Unresponsive: the line stands, the host says nothing. It gets no new
	// sandboxes in this state (see answering), and that is worth a word of its
	// own — "connected" beside an agent that does not start is the sentence
	// that sends somebody looking in the wrong place.
	Unresponsive bool `json:"unresponsive,omitempty"`
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
		tags, images := c.effective()
		reportedTags, reportedImages := c.reported()
		out[c.runnerID] = Live{
			RunnerID: c.runnerID, Connected: true, Protocol: c.protocol,
			Version: c.version, Arch: c.arch, Tags: tags, Images: images,
			ReportedTags: reportedTags, ReportedImages: reportedImages,
			Features:  c.features,
			Sandboxes: running, MaxSandboxes: c.maxSandboxes, Outdated: c.protocol < Protocol,
			Unresponsive: !c.answering(),
		}
	}
	return out
}

// CapacityView is what a host last said about its disk, with the moment it said
// it. The moment belongs to the figure: a remembered number without an age is
// the kind that reassures right up to the moment the disk is full, and one with
// an age can be read for what it is — current, or from before the host became
// busy.
type CapacityView struct {
	CapacityReport
	MeasuredAt time.Time `json:"measured_at"`
}

// Capacity is the last figure a runner reported about itself. It costs no round
// trip: the connection refreshes it in the background (capacityWatch), which is
// what keeps a view of many hosts from being the sum of their slowness. false
// means this runner is not connected, or has not answered once yet.
func (p *Pool) Capacity(runnerID uuid.UUID) (CapacityView, bool) {
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()
	if c == nil {
		return CapacityView{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capacityAt.IsZero() {
		return CapacityView{}, false
	}
	return CapacityView{CapacityReport: c.capacity, MeasuredAt: c.capacityAt}, true
}

// Update tells a host to replace its own binary. Long, because it downloads
// tens of megabytes over whatever line that host has — and asked for, so
// somebody is watching.
//
// The answer comes back BEFORE the runner restarts. That is what makes the
// difference between "installed, coming back" and "fell over" readable at all:
// after the restart there is nothing left to ask.
func (p *Pool) Update(ctx context.Context, orgID, runnerID uuid.UUID, version, baseURL string) (UpdateResult, error) {
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()
	if c == nil || c.orgID != orgID {
		// A runner of another organisation is not connected as far as this
		// caller is concerned: this message hands the host a URL to fetch and
		// run a binary from, and the organisation is the runner's, not the
		// request's (#159).
		return UpdateResult{}, ErrNoRunner
	}
	if c.builtin {
		// The built-in runner is this process. It is updated by updating the
		// control plane, and pretending otherwise would offer a button that
		// downloads a binary nobody would ever start.
		return UpdateResult{}, fmt.Errorf("%w: the built-in runner is updated with the control plane", ErrNotSupported)
	}
	if !hasAll(c.features, []string{FeatureSelfUpdate}) {
		// The one case where silence would be the answer: a build from before
		// this message existed ignores it, and the caller would wait out the
		// whole timeout for something that is never coming. Said in a sentence
		// instead, with what to do about it.
		return UpdateResult{}, fmt.Errorf("%w: this runner is older than the self-update — install it once by hand "+
			"(curl -fsSL <this server>/install.sh | sh -s -- --runner), after that the button works", ErrNotSupported)
	}
	// One at a time, and out of the scheduler's sight while it runs: the host
	// is about to exec, and a start accepted in the meantime would be
	// abandoned by it (#161).
	if !c.setUpdating(true) {
		return UpdateResult{}, fmt.Errorf("an update is already under way on runner %s", short(runnerID))
	}
	defer c.setUpdating(false)
	answer, err := c.ask(ctx, TypeUpdate, Update{Version: version, BaseURL: baseURL}, updateTimeout)
	if err != nil {
		return UpdateResult{}, err
	}
	return decode[UpdateResult](answer)
}

// updateTimeout bounds one update. Generous for the same reason a start is: it
// is a download, and a slow line is slow rather than broken.
const updateTimeout = 15 * time.Minute

// ErrNotSupported: the request is fine, this host is the wrong addressee.
var ErrNotSupported = errors.New("not supported for this runner")

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
