package orchestrator

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/observability"
	"covey/internal/voice"
)

// covey/correction: the agent reports that a person rewrote a text it had
// published (spec/24).
//
// This is the second source of correction pairs, and the only one that can
// reach the texts an agent actually sent out. The first — a reviewer editing at
// the approval gate — lives inside the control plane and needs nobody's help.
// This one cannot: the plugins run in the sandbox, get their own target
// system's credential and nothing else, and have no way back to the platform
// (target.System is Name/ActionSubject/Execute/PromptDoc, and COVEY_ACTION_PORT
// appears in neither the SDK nor the pack). The agent is the only party that
// can both read what stands there now and speak to the platform — so the agent
// files the pair.
//
// That it is the agent's word is a real weakness and it is answered the way the
// wiki answers the same weakness: the action has its own guard-rail subject,
// every pair is in the recording, and a person can throw one out on the voice's
// page. What it must not do is take anything it is given.

// correctionAction files one pair against the voice the agent carries.
func (o *Orchestrator) correctionAction(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring, ok func(any) daemon.InjectHiring,
	fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	if o.Voices == nil {
		return fail("voices are not configured on this installation")
	}
	// A pair belongs to a voice. An agent without one has nowhere to put it,
	// and saying so beats storing it against nothing.
	voiceID, carried := o.Voices.VoiceOfAgent(ctx, agent.ID)
	if !carried {
		return fail("you carry no voice, so there is nothing for a correction to improve — " +
			"a voice is assigned in the agent's settings")
	}
	// WHO changed it decides whether this is a correction at all. Only a
	// person's edit is one: a colleague rewriting the text is a handover, and
	// storing that would teach the voice to imitate itself.
	by := strings.TrimSpace(req.By)
	if by == "" {
		return fail("name who changed the text (\"by\"): only a person's edit is a correction — " +
			"a colleague's rewrite is a handover")
	}
	if colleague, err := o.Registry.GetBySlug(ctx, agent.OrgID, strings.ToLower(by)); err == nil {
		return fail("%s is an agent of this organisation, so that is a handover and not a correction — "+
			"a voice that learns from a colleague learns to imitate itself", colleague.Slug)
	}
	if good, why := voice.IsCorrection(req.Before, req.After); !good {
		return fail("%s", why)
	}

	agentID := agent.ID
	stored, err := o.Voices.AddCorrection(ctx, agent.OrgID, voiceID, voice.Correction{
		AgentID: &agentID, Source: voice.SourceTarget,
		Action: strings.TrimSpace(req.Where), Before: req.Before, After: req.After,
	})
	if err != nil {
		return fail("the correction could not be stored: %v", err)
	}
	// Into the recording, like every other action of consequence: a pair that
	// steers how every agent on this voice writes has to be findable afterwards
	// without opening the voice.
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "correction", "voice": voiceID.String(),
			"where": strings.TrimSpace(req.Where), "by": by})
	return ok(map[string]any{"stored": true, "correction_id": stored.ID.String(),
		"note": "the pair goes into the next build of this voice"})
}
