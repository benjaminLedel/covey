// Package workflowhost runs covey's workflows inside the control plane
// (#563). It holds one engine per stored workflow, keeps their runs in the
// database as they change, hands colleague steps to agents as backlog tasks,
// and moves a run on when such a task ends.
//
// What the engine asks of the world it gets here: whether a colleague can
// work (the registry), a task for a step (the backlog). An approval waits
// in the workflow's views until the chat carries it. Messages, AI steps and
// target-system actions are refused with a clear error until they are
// connected; the run then stops and says why.
package workflowhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
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
// the chat carries workflows (#441) the host has none: approvals wait in the
// workflow's views, messages are refused.
type Messenger interface {
	Ask(ctx context.Context, orgID uuid.UUID, q workflow.Question) error
	Notify(ctx context.Context, orgID uuid.UUID, m workflow.Message) error
}

// Platform is what a workflow borrows from the orchestrator (#566): the
// heartbeat's gate, and an action run as a colleague under its rules.
type Platform interface {
	HasWork(ctx context.Context, orgID, agentID uuid.UUID, condition string) (bool, string)
	// SignsItems: the system's gate signature lists its waiting items
	// (target.ItemSigner), so a firing can run one case per item.
	SignsItems(ctx context.Context, orgID uuid.UUID, system string) bool
	ActAs(ctx context.Context, agent agents.Agent, system, action string, params json.RawMessage, pending string) (string, error)
}

type Host struct {
	Store     *workflow.Store
	Backlog   *backlog.Store
	Agents    *agents.Registry
	Platform  Platform  // nil: gates fire, actions refuse
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
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	h.fireSchedules(ctx)
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

var (
	// ErrStillOn: a workflow that watches or steers is not deleted; it is
	// switched off first, so that nothing starts while it goes.
	ErrStillOn = errors.New("the workflow is not off — switch it off before deleting it")
	// ErrStepsWorking: a run stands at a colleague step, and its task is in an
	// agent's backlog. Deleting the workflow would leave that task reporting
	// to nothing.
	ErrStepsWorking = errors.New("a colleague is still working on a step of this workflow")
)

// Delete removes a workflow that is off and has no colleague at work on it.
// Runs that only watch, queue or wait go with it. A workflow the host could
// not load has no runs to ask about and is removed as it is.
func (h *Host) Delete(ctx context.Context, orgID uuid.UUID, slug string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var id uuid.UUID
	for wid, hw := range h.engines {
		if hw.stored.OrgID != orgID || hw.stored.Slug != slug {
			continue
		}
		if hw.stored.Def.Mode != workflow.ModeOff {
			return ErrStillOn
		}
		for _, r := range hw.eng.Runs() {
			if r.State == workflow.StateWorking {
				return ErrStepsWorking
			}
		}
		id = wid
	}
	if err := h.Store.Delete(ctx, orgID, slug); err != nil {
		return err
	}
	delete(h.engines, id)
	return nil
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

// Act runs the action as the colleague the step names (#566), through the
// orchestrator's own path for an agent's action.
func (o *outside) Act(a workflow.Action) error {
	if o.h.Platform == nil {
		return fmt.Errorf("actions in target systems are %w", ErrNotConnected)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	agent, err := o.h.colleague(ctx, o.orgID, o.workflowID, a.As)
	if err != nil {
		return err
	}
	pending, err := o.h.Platform.ActAs(ctx, agent, a.System, a.Do, paramsJSON(a.Params), a.Pending)
	if err != nil {
		return err
	}
	if pending != "" {
		return &workflow.PendingError{Approval: pending}
	}
	return nil
}

// paramsJSON passes the parameters as the plugin reads them: a value that is
// a whole number or true/false is passed as one, everything else as text —
// "issue_iid": "12" would not unmarshal into an int.
func paramsJSON(params map[string]string) json.RawMessage {
	out := make(map[string]any, len(params))
	for k, v := range params {
		switch {
		case v == "true" || v == "false":
			out[k] = v == "true"
		case isInt(v):
			n, _ := strconv.ParseInt(v, 10, 64)
			out[k] = n
		default:
			out[k] = v
		}
	}
	raw, _ := json.Marshal(out)
	return raw
}

func isInt(v string) bool {
	if v == "" || len(v) > 18 {
		return false
	}
	_, err := strconv.ParseInt(v, 10, 64)
	return err == nil && !strings.HasPrefix(v, "+") && (v == "0" || !strings.HasPrefix(strings.TrimPrefix(v, "-"), "0"))
}

// colleague resolves `as`: a role of the workflow (its first available
// member) or an agent slug — hired and not stopped either way.
func (h *Host) colleague(ctx context.Context, orgID, workflowID uuid.UUID, as string) (agents.Agent, error) {
	h.mu.Lock()
	hw := h.engines[workflowID]
	h.mu.Unlock()
	names := []string{as}
	if hw != nil {
		if members, ok := hw.stored.Def.Roles[as]; ok {
			names = members
		}
	}
	for _, name := range names {
		a, err := h.Agents.GetBySlug(ctx, orgID, name)
		if err == nil && !a.Draft() && !a.Killed {
			return a, nil
		}
	}
	return agents.Agent{}, fmt.Errorf("nobody can act as %q: no hired, running agent", as)
}

func (o *outside) Ask(q workflow.Question) error {
	// Without a chat the question still stands: the run waits at the
	// approval where the workflow's views show it, and the person decides
	// there (#564). Nothing claims it was delivered anywhere else.
	if o.h.Messenger == nil {
		return nil
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
