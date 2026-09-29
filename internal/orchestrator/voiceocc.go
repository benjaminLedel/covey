package orchestrator

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/observability"
	"covey/internal/style"
	"covey/internal/voice"
)

/* The voice of a run (#471).
 *
 * A run writes for one occasion (voice.TaskOccasion): the chat when its task
 * came from a conversation — the result is read out there — otherwise
 * customers, or publications when the task says so in a line "occasion:
 * publications". The voice the rule chooses for that occasion and audience
 * becomes the run's TONE.md, rendered from the voice as it stands now: the
 * slot is the assignment, and it acts at the moment of writing, not at the
 * moment somebody saved a config.
 *
 * When no level names a voice, the config's own TONE.md stays — what every
 * agent without a slot has carried all along, so nothing changes for it. */

// runVoice is what the dispatch resolved for one task.
type runVoice struct {
	occasion voice.Occasion
	choice   voice.Choice
	voice    *voice.Voice
}

// taskAudience is whom a task's text is for: for a task from a conversation,
// the person who asked and the others who wrote there last; for any other
// task nobody the org chart knows — a customer has no department.
func (o *Orchestrator) taskAudience(ctx context.Context, agent agents.Agent, task backlog.Task) voice.Audience {
	if task.ConversationID == nil || !strings.HasPrefix(task.Origin, "chat:") {
		return voice.Audience{}
	}
	return o.Voices.ConversationAudience(ctx, agent.OrgID, *task.ConversationID, strings.TrimPrefix(task.Origin, "chat:"))
}

// resolveRunVoice chooses the voice for a task and an occasion. A voice that
// is not assignable (any more) is treated as none: nothing of it may act.
func (o *Orchestrator) resolveRunVoice(ctx context.Context, agent agents.Agent, task backlog.Task, occ voice.Occasion) runVoice {
	rv := runVoice{occasion: occ}
	if o.Voices == nil {
		return rv
	}
	rv.choice = o.Voices.Resolve(ctx, agent.OrgID, agent.ID, occ, o.taskAudience(ctx, agent, task))
	if rv.choice.Found() {
		if v, err := o.Voices.Get(ctx, agent.OrgID, rv.choice.VoiceID); err == nil && v.Assignable() {
			rv.voice = &v
		}
	}
	return rv
}

// runVoiceFiles is the config as the run's prompt is compiled from it: the
// TONE.md replaced by the chosen voice when there is one, and the audience
// block that goes after the prompt. The recording names the voice and why —
// also when there was none, so that "why did it write like that" has an
// answer in the run itself.
func (o *Orchestrator) runVoiceFiles(ctx context.Context, agent agents.Agent, task backlog.Task, files map[string]string) (map[string]string, string) {
	occ := voice.TaskOccasion(task.ConversationID != nil, task.Title, task.Body)
	rv := o.resolveRunVoice(ctx, agent, task, occ)
	out := make(map[string]string, len(files)+1)
	for k, v := range files {
		out[k] = v
	}
	data := map[string]any{"voice_occasion": string(occ), "voice_reason": rv.choice.Reason()}
	if rv.voice != nil {
		out["TONE.md"] = voice.Render(*rv.voice)
		data["voice_id"], data["voice"] = rv.voice.ID.String(), rv.voice.Name
	} else {
		data["voice_reason"] = voice.Choice{Occasion: occ, Level: voice.LevelNone}.Reason()
		if strings.TrimSpace(files["TONE.md"]) != "" {
			data["tone"] = "the config's own TONE.md"
		}
	}
	var names []string
	for _, n := range rv.choice.Notes {
		names = append(names, n.Department)
	}
	if len(names) > 0 {
		data["audience"] = strings.Join(names, ", ")
	}
	if o.Obs != nil && o.Voices != nil {
		taskID := task.ID
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle, data)
	}
	return out, voice.AudiencePrompt(rv.choice.Notes)
}

// outwardProfiles are the style profiles a text leaving through a target
// system during a task is measured against: the profile of the voice the
// task's outward occasion resolves to (voice.OutwardOccasion — a mail sent
// from a chat task still goes to a customer), then those of the config's
// other Markdown files. Without a measured voice there, the config's own, as
// before #471. ok false means no voice was resolved and the caller keeps
// its own lookup.
func (o *Orchestrator) outwardProfiles(ctx context.Context, agent agents.Agent, taskID uuid.UUID) (profiles []style.Profile, described bool, ok bool) {
	if o.Voices == nil || o.Backlog == nil {
		return nil, false, false
	}
	task, err := o.Backlog.Get(ctx, taskID)
	if err != nil {
		return nil, false, false
	}
	occ := voice.OutwardOccasion(voice.TaskOccasion(task.ConversationID != nil, task.Title, task.Body))
	rv := o.resolveRunVoice(ctx, agent, task, occ)
	if rv.voice == nil {
		return nil, false, false
	}
	if !rv.voice.Measured() {
		return nil, true, true
	}
	profiles = []style.Profile{rv.voice.Profile}
	if cfg, err := o.Registry.CurrentConfig(ctx, agent.ID); err == nil {
		profiles = append(profiles, otherProfiles(cfg.Files)...)
	}
	return profiles, false, true
}
