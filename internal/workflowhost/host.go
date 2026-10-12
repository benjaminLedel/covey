// Package workflowhost runs covey's workflows inside the control plane
// (#563). It holds one engine per stored workflow, keeps their runs in the
// database as they change, hands colleague steps to agents as backlog tasks,
// and moves a run on when such a task ends.
//
// What the engine asks of the world it gets here: whether a colleague can
// work (the registry), a task for a step (the backlog). Approvals, messages,
// AI steps and target-system actions are refused with a clear error until
// they are connected; the run then stops and says why, rather than
// pretending a question reached someone.
package workflowhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/workflow"
)

// OriginPrefix marks a backlog task as a workflow step:
// workflow:<workflow id>:<run id>:<step id>.
const OriginPrefix = "workflow:"

// ErrNotConnected is what a building block answers that is not wired yet.
var ErrNotConnected = errors.New("not connected yet")

// Messenger reaches people: an approval to decide, a message to read. Until
// the chat carries workflows (#441), the host has none and refuses both.
type Messenger interface {
	Ask(ctx context.Context, orgID uuid.UUID, q workflow.Question) error
	Notify(ctx context.Context, orgID uuid.UUID, m workflow.Message) error
}

type Host struct {
	Store     *workflow.Store
	Backlog   *backlog.Store
	Agents    *agents.Registry
	Messenger Messenger // may be nil
	Log       *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	engines map[uuid.UUID]*hosted // by workflow id
}

type hosted struct {
	stored workflow.Stored
	eng    *workflow.Engine
}

// Load builds an engine for every stored workflow and restores its runs. A
// workflow that does not load is logged and left out; the others run.
func (h *Host) Load(ctx context.Context) error {
	all, err := h.Store.ListAll(ctx)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.engines = map[uuid.UUID]*hosted{}
	for _, st := range all {
		hw, err := h.build(ctx, st)
		if err != nil {
			h.log().Warn("workflow not loaded", "org", st.OrgID, "workflow", st.Slug, "version", st.Version, "err", err)
			continue
		}
		h.engines[st.ID] = hw
	}
	return nil
}

// Reload takes up the current version of one workflow after it was saved.
// Its runs carry on under the new version; if they cannot — a step they
// stand at is gone — the old version keeps running and the error says so.
func (h *Host) Reload(ctx context.Context, orgID uuid.UUID, slug string) error {
	st, err := h.Store.Get(ctx, orgID, slug)
	if err != nil {
		return err
	}
	hw, err := h.build(ctx, st)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.engines == nil {
		h.engines = map[uuid.UUID]*hosted{}
	}
	h.engines[st.ID] = hw
	return nil
}

func (h *Host) build(ctx context.Context, st workflow.Stored) (*hosted, error) {
	eng, err := workflow.New(st.Def, &outside{h: h, orgID: st.OrgID, workflowID: st.ID}, h.now)
	if err != nil {
		return nil, err
	}
	runs, err := h.Store.Runs(ctx, st.ID)
	if err != nil {
		return nil, err
	}
	if err := eng.Restore(runs); err != nil {
		return nil, err
	}
	id := st.ID
	eng.OnChange(func(rs []workflow.Run) {
		// Written with a context of its own: the call that changed the runs
		// may have been cancelled the moment it returned.
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.Store.SaveRuns(c, id, rs); err != nil {
			h.log().Error("workflow runs not saved", "workflow", id, "err", err)
		}
	})
	return &hosted{stored: st, eng: eng}, nil
}

// Engine returns the engine of a workflow, for starting runs and reading
// them.
func (h *Host) Engine(orgID uuid.UUID, slug string) (*workflow.Engine, workflow.Stored, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, hw := range h.engines {
		if hw.stored.OrgID == orgID && hw.stored.Slug == slug {
			return hw.eng, hw.stored, true
		}
	}
	return nil, workflow.Stored{}, false
}

// TaskEnded is the backlog's completion hook for workflow steps. A task that
// failed but was continued (the turn limit) is not the end of its step; the
// continuation reports for it.
func (h *Host) TaskEnded(ctx context.Context, t backlog.Task) {
	if t.State == backlog.StateFailed {
		if more, err := h.Backlog.HasContinuation(ctx, t.ID); err == nil && more {
			return
		}
	}
	origin, report, ok, err := h.Backlog.ChainReport(ctx, t.ID, OriginPrefix)
	if err != nil {
		h.log().Warn("workflow step: chain not readable", "task", t.ID, "err", err)
		return
	}
	if !ok {
		return
	}
	workflowID, runID, step, ok := parseOrigin(origin)
	if !ok {
		h.log().Warn("workflow step: origin not understood", "task", t.ID, "origin", origin)
		return
	}
	h.mu.Lock()
	hw := h.engines[workflowID]
	h.mu.Unlock()
	if hw == nil {
		h.log().Warn("workflow step ended for a workflow that is not loaded", "task", t.ID, "workflow", workflowID)
		return
	}
	run, found := hw.eng.Run(runID)
	if !found || run.Step != step || run.State != workflow.StateWorking {
		// The run moved on meanwhile — reassigned, decided, restored to
		// another version. A late answer is not a new one.
		return
	}
	if t.State != backlog.StateDone {
		reason := "the task failed"
		if t.Error != nil && strings.TrimSpace(*t.Error) != "" {
			reason += ": " + strings.TrimSpace(*t.Error)
		}
		h.stepFailed(hw, runID, step, reason)
		return
	}
	switch err := hw.eng.Done(runID, report); {
	case errors.Is(err, workflow.ErrIncomplete):
		h.stepFailed(hw, runID, step, fmt.Sprintf("the task ended without reporting what the step asks for (%v)", err))
	case err != nil:
		h.log().Warn("workflow step: result not taken", "run", runID, "step", step, "err", err)
	}
}

func (h *Host) stepFailed(hw *hosted, runID, step, reason string) {
	if err := hw.eng.StepFailed(runID, step, reason); err != nil {
		h.log().Warn("workflow step: failure not recorded", "run", runID, "step", step, "err", err)
	}
}

// Tick runs every engine's clock once: limits, waits, required steps that
// may have found someone.
func (h *Host) Tick() {
	h.mu.Lock()
	engines := make([]*workflow.Engine, 0, len(h.engines))
	for _, hw := range h.engines {
		engines = append(engines, hw.eng)
	}
	h.mu.Unlock()
	for _, e := range engines {
		e.Tick()
	}
}

// Run ticks every interval until ctx ends.
func (h *Host) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Tick()
		}
	}
}

// Runs lists the runs of one workflow, oldest first.
func (h *Host) Runs(orgID uuid.UUID, slug string) []workflow.Run {
	e, _, ok := h.Engine(orgID, slug)
	if !ok {
		return nil
	}
	runs := e.Runs()
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Seq() < runs[j].Seq() })
	return runs
}

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Host) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func origin(workflowID uuid.UUID, runID, step string) string {
	return OriginPrefix + workflowID.String() + ":" + runID + ":" + step
}

func parseOrigin(o string) (workflowID uuid.UUID, runID, step string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(o, OriginPrefix), ":")
	if len(parts) != 3 {
		return uuid.Nil, "", "", false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, "", "", false
	}
	return id, parts[1], parts[2], true
}

// outside is what one workflow's engine sees of covey.
type outside struct {
	h          *Host
	orgID      uuid.UUID
	workflowID uuid.UUID
}

func (o *outside) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// Available: a hired agent of the organisation that is not stopped. Names
// in a role are agent slugs.
func (o *outside) Available(name string) bool {
	ctx, cancel := o.ctx()
	defer cancel()
	a, err := o.h.Agents.GetBySlug(ctx, o.orgID, name)
	return err == nil && !a.Draft() && !a.Killed
}

// Dispatch puts the step into the agent's backlog. The task says which
// fields to report, and how; a step that asks for none ends with the task.
func (o *outside) Dispatch(t workflow.Task) error {
	if t.Assignee == "" {
		return fmt.Errorf("AI steps are %w", ErrNotConnected)
	}
	ctx, cancel := o.ctx()
	defer cancel()
	a, err := o.h.Agents.GetBySlug(ctx, o.orgID, t.Assignee)
	if err != nil {
		return err
	}
	title := stepTitle(o.h, o.workflowID, t.Step)
	if t.Case != "" {
		title += " · " + t.Case
	}
	_, err = o.h.Backlog.Create(ctx, o.orgID, a.ID, title, taskBody(t), origin(o.workflowID, t.Run, t.Step), 3)
	return err
}

func (o *outside) Act(a workflow.Action) error {
	return fmt.Errorf("actions in target systems are %w", ErrNotConnected)
}

func (o *outside) Ask(q workflow.Question) error {
	if o.h.Messenger == nil {
		return fmt.Errorf("approvals are %w", ErrNotConnected)
	}
	ctx, cancel := o.ctx()
	defer cancel()
	return o.h.Messenger.Ask(ctx, o.orgID, q)
}

func (o *outside) Notify(m workflow.Message) error {
	if o.h.Messenger == nil {
		return fmt.Errorf("messages are %w", ErrNotConnected)
	}
	ctx, cancel := o.ctx()
	defer cancel()
	return o.h.Messenger.Notify(ctx, o.orgID, m)
}

func stepTitle(h *Host, workflowID uuid.UUID, step string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hw := h.engines[workflowID]; hw != nil {
		if s := hw.stored.Def.Steps[step]; s != nil && s.Title != "" {
			return s.Title
		}
	}
	return step
}

// taskBody is the step's task, and — when the step asks for fields — how to
// report them. Without them the workflow cannot decide where the case goes.
func taskBody(t workflow.Task) string {
	if len(t.Result) == 0 {
		return t.Text
	}
	example := make([]string, len(t.Result))
	for i, f := range t.Result {
		example[i] = fmt.Sprintf("%q:\"…\"", f)
	}
	return t.Text + "\n\n---\nThis task is a step of a workflow. Before you finish, report its result with covey/report: " +
		"{" + strings.Join(example, ",") + "}. Without these fields the workflow stops at this step."
}
