package orchestrator

// Review: the meta-actions that let an agent read a colleague and propose a
// change for him (spec/21-operations-and-improvement.md).
//
// The other half of hiring.go, deliberately with its own scope:
// `- system: covey scope: agents:review`. It frees these three actions and
// NOT the designing; `agents:write` frees the designing and not these. An
// agent may hold both — the HR department and the covey Doctor do not, and
// therefore neither of the two can do the other's work with the other's
// access.
//
// Five rules hold them up, all five enforced here and not in the prompt:
//
//  1. HE PROPOSES, HE DOES NOT PUT IN FORCE. propose_agent_config
//     writes an inactive version. There is no path from here to a
//     running config — for no file. Rule 4 from spec/20 stays
//     untouched: set_agent_config is not widened, the new action is
//     strictly weaker. A compromised covey Doctor produces a queue of
//     bad proposals that a human rejects — an annoyance, not an
//     incident.
//  2. HE DOES NOT READ HIS OWN NUMBERS. work_record does not reach the
//     caller. That is the same reason KPIS.md is not compiled into the
//     system prompt (internal/agents/kpi.go): whoever knows what he is
//     measured on works toward the measure instead of the thing.
//
//     Proposing to yourself is allowed by contrast — a deliberate
//     departure from the first version of the rule. The reason that
//     carried it does not carry here: nothing from here runs, a human
//     accepts or rejects every proposal. That also closes the open
//     point from spec/20 — the HR department may propose its own
//     configuration after its self-onboarding. With `agents:write`
//     alone, though, ONLY its own: for a colleague's it takes
//     `agents:review`, otherwise the second scope would be bypassed.
//  3. HE READS FACTS. The work record is what the Control Plane itself
//     wrote down. A conversation — a recording — is reachable only
//     through an approval, one run at a time, and the approval is tied
//     to exactly this run.
//  4. NOTHING ELSE ABOUT A COLLEAGUE is reachable: not his secrets, not
//     his guard rails, not his runtime, not his budget, not his
//     emergency stop. There is no action for it.
//  5. THERE IS NO `fire`. Not a forbidden one — a missing one, so there
//     is nothing to forget. This agent may say that a colleague is not
//     working. Ending the employment is the act of a human, just as its
//     beginning is.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/observability"
	"covey/internal/workrecord"
)

// maxRecordingEvents limits what ONE run returns. A recording is the
// expensive part of the work record, and a run that goes over this limit
// is itself the finding.
const maxRecordingEvents = 400

// reviewTarget resolves the agent this is about — within one's own
// organisation. allowSelf separates the two halves of rule 2: one's own
// work record stays closed (he should not know his numbers), one's own
// proposal is open (a human decides it either way).
func (o *Orchestrator) reviewTarget(ctx context.Context, agent agents.Agent, slug string,
	allowSelf bool) (agents.Agent, string) {

	slug = strings.TrimSpace(slug)
	if slug == "" {
		return agents.Agent{}, "agent is missing (the slug of the agent this is about)"
	}
	other, err := o.Registry.GetBySlug(ctx, agent.OrgID, slug)
	if err != nil {
		return agents.Agent{}, "no agent \"" + slug + "\" in this organisation"
	}
	if other.ID == agent.ID && !allowSelf {
		return agents.Agent{}, "you do not read your own record — an agent that knows " +
			"what it is measured on works towards the measure. Read a colleague's."
	}
	return other, ""
}

// reviewWorkRecord hands out the work record of a colleague: facts the
// Control Plane itself wrote down, per agent and period.
func (o *Orchestrator) reviewWorkRecord(ctx context.Context, agent agents.Agent, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent, false)
	if reason != "" {
		return fail("%s", reason)
	}
	days := req.Days
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	builder := &workrecord.Builder{Pool: o.Pool, Registry: o.Registry, Obs: o.Obs, Skills: o.lintSkills()}
	rec, err := builder.Build(ctx, other.ID, time.Now().AddDate(0, 0, -days))
	if err != nil {
		return fail("work record not readable: %v", err)
	}
	return ok(rec)
}

// reviewReadRecording gives out ONE run in so many words — and only after a
// human has agreed.
//
// The approval comes into being before this function (hiring.go,
// AlwaysApprove); whoever gets here has it. What is left is the check that
// the run belongs to the colleague who was asked about: the approval names
// an agent and a task, and both have to match, otherwise a human approved
// something other than what he read.
func (o *Orchestrator) reviewReadRecording(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent, false)
	if reason != "" {
		return fail("%s", reason)
	}
	readID, err := uuid.Parse(strings.TrimSpace(req.Task))
	if err != nil {
		return fail("task is missing or not an id (the run you want to read; the work record names it)")
	}
	task, err := o.Backlog.Get(ctx, readID)
	if err != nil || task.AgentID != other.ID {
		return fail("no run %q at agent %q", req.Task, other.Slug)
	}

	events, err := o.Obs.Events(ctx, other.ID, &readID, 0, maxRecordingEvents)
	if err != nil {
		return fail("recording not readable: %v", err)
	}
	// The reading itself belongs in the recording of the READER — and written
	// by the Control Plane, not by the sandbox proxy. Whoever reads the work
	// record of a covey Doctor must see which conversations he looked into;
	// otherwise you judge him on what he wrote without knowing what he
	// read. As a lifecycle event like the design actions: the proxy writes
	// the action itself, the platform writes the provenance.
	//
	// The entry hangs on ONE'S OWN task (taskID), not on the run that was
	// read (readID): Obs.Events always filters by agent_id AND task_id, so an
	// entry under a foreign task would be invisible in the reader's recording
	// — and the reference would lead into a run that does not belong to him.
	// Which run was read stands next to it in "run".
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "recording_read", "about_agent": other.ID.String(),
			"slug": other.Slug, "run": readID.String(),
			"events": strconv.Itoa(len(events))})

	out := map[string]any{
		"agent": other.Slug, "task": readID.String(), "title": task.Title,
		"state": task.State, "events": events,
	}
	if len(events) == maxRecordingEvents {
		out["note"] = "truncated to the last 400 events of this run"
	}
	return ok(out)
}

// reviewPropose writes a proposal: a stored config version that is NOT in
// effect. A human accepts it, or it stays where it is.
func (o *Orchestrator) reviewPropose(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring, ok func(any) daemon.InjectHiring,
	fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent, true)
	if reason != "" {
		return fail("%s", reason)
	}
	// The self-proposal is the one exception that `agents:write` alone
	// carries (spec/20): whoever designs may, after his self-onboarding,
	// propose his own configuration. For a colleague's it takes the
	// review scope — otherwise the HR department would have fetched,
	// through the back door, exactly the reach two scopes should prevent.
	if other.ID != agent.ID && !o.mayUsecovey(ctx, agent, scopeReview) {
		return fail("%s", "with `scope: "+scopeWrite+"` you may propose only your OWN "+
			"configuration — proposing for a colleague needs `scope: "+scopeReview+"`")
	}
	if len(req.Files) == 0 {
		return fail("files is missing (file name → complete content, only the files you change)")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return fail("title is missing (one line: what you propose)")
	}
	if strings.TrimSpace(req.Rationale) == "" {
		return fail("rationale is missing — a proposal without the observation behind it " +
			"is one a human cannot decide on")
	}
	// Rule 2 from spec/20 applies here too: a proposal may not give a
	// colleague the platform's own system. Otherwise the way around rule
	// 2 would be an accepted proposal.
	if acc, ok := req.Files["ACCESS.md"]; ok {
		for _, a := range agents.ParseAccess(acc) {
			if a.System == hiringSystem {
				return fail("a proposal may not give a colleague the system `covey` — " +
					"who may reach the platform itself is decided by a human, not proposed")
			}
		}
	}

	item, err := o.Registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: agent.OrgID, AgentID: other.ID, Kind: agents.KindProposal,
		Title: title, Rationale: req.Rationale, Files: req.Files,
		AuthorAgentID: &agent.ID, TaskID: &taskID,
	})
	if err != nil {
		return fail("%v", err)
	}
	o.notifyImprovement(ctx, item)
	// Provenance is written by the platform, not the model: which task a
	// proposal came from stands here and not in a message.
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "config_proposed", "about_agent": other.ID.String(),
			"slug": other.Slug, "proposal": item.ID.String()})

	return ok(map[string]any{
		"proposal": item.ID.String(), "agent": other.Slug,
		"base_version": item.BaseVersion, "files": sortedKeys(req.Files),
		"note": "The proposal is stored and NOT in effect. A human accepts it, or it stays where it is.",
	})
}

// reviewWrite records the assessment — and with it the points that came out
// of it and that only a human can carry out.
//
// ONE call for both, because it is one judgement: the text says what it saw,
// the finding says who has to fix it, the issue says where it already lies.
// In two calls this falls apart into a report without consequences and
// consequences without one — between the two a run can end at the turn limit.
//
// The review waits for nothing. It does not go into the stock of open
// points, but onto the colleague's page; the findings and issues go into
// both, because they need someone.
func (o *Orchestrator) reviewWrite(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring, ok func(any) daemon.InjectHiring,
	fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent, false)
	if reason != "" {
		return fail("%s", reason)
	}
	if strings.TrimSpace(req.Summary) == "" {
		return fail("summary is missing — the review IS the text; a colleague " +
			"with three proposals and no assessment is a diff without a diagnosis")
	}
	days := req.Days
	if days <= 0 {
		days = 30
	}
	now := time.Now()
	rev, err := o.Registry.CreateReview(ctx, agents.Review{
		OrgID: agent.OrgID, AgentID: other.ID, AuthorAgentID: &agent.ID, TaskID: &taskID,
		PeriodFrom: now.AddDate(0, 0, -days), PeriodTo: now, Summary: req.Summary,
	})
	if err != nil {
		return fail("%v", err)
	}

	// Findings and issues as open points. A finding nobody has to tick off
	// is a message — and messages get lost (spec/21).
	angelegt := 0
	for _, spec := range []struct {
		kind  string
		notes []daemon.ReviewNote
	}{{agents.KindFinding, req.Findings}, {agents.KindIssue, req.Issues}} {
		for _, n := range spec.notes {
			if strings.TrimSpace(n.Title) == "" {
				continue
			}
			item, err := o.Registry.CreateImprovement(ctx, agents.ImprovementItem{
				OrgID: agent.OrgID, AgentID: other.ID, Kind: spec.kind,
				Title: n.Title, Rationale: n.Detail, Link: n.Link,
				AuthorAgentID: &agent.ID, TaskID: &taskID,
			})
			if err != nil {
				o.Log.Warn("review: item not stored", "agent", other.Slug, "kind", spec.kind, "err", err)
				continue
			}
			o.notifyImprovement(ctx, item)
			angelegt++
		}
	}

	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "review_written", "about_agent": other.ID.String(),
			"slug": other.Slug, "review": rev.ID.String(), "items": strconv.Itoa(angelegt)})

	return ok(map[string]any{
		"review": rev.ID.String(), "agent": other.Slug, "items": angelegt,
		"note": "The review is on the colleague's profile, dated. It does not reach the " +
			"colleague itself by any path — findings and issues wait for a human.",
	})
}

// lintSkills gives the config lint of the work record the agent's skills.
// Without them it would check half a config: procedures that moved out of
// PLAYBOOKS.md into a skill would be invisible, and rules like "whoever
// works, comments" would fire wrongly — a check that nags good configs
// gets ignored.
//
// nil when the instance runs without a skill store; the rules that need
// them then fall away.
func (o *Orchestrator) lintSkills() agents.SkillLookup {
	if o.Skills == nil {
		return nil
	}
	return func(ctx context.Context, orgID, agentID uuid.UUID) (map[string]string, error) {
		found, err := o.Skills.ForAgent(ctx, orgID, agentID)
		if err != nil {
			return nil, err
		}
		out := make(map[string]string, len(found))
		for _, sk := range found {
			var b strings.Builder
			for _, f := range sk.Files {
				b.WriteString(f.Content)
				b.WriteString("\n")
			}
			out[sk.Name] = b.String()
		}
		return out, nil
	}
}
