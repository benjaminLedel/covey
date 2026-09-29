package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/observability"
	"covey/internal/voice"
)

// covey/voice_draft: an agent drafts a voice for a person to release (#458).
//
// The third way a voice comes into being, beside texts uploaded and a
// description typed on the voices page: somebody asks an agent in the team
// chat, the agent collects texts or interviews them in a few questions, and
// files the result. It files a DRAFT, and that is the whole permission model:
//
//   - the voice is created with no card released and no agent carrying it —
//     nothing of it reaches any prompt;
//   - there is no op to release, build or assign; those stay on the voices
//     page, with the manage roles;
//   - the agent is named on the voice (drafted_by), and the action is in the
//     recording.
//
// No model call happens here. A description is stored as the agent wrote it,
// and a person writes the card from it on the voices page — one click, whose
// cost is theirs to decide. Texts are stored as the corpus; a person builds.

// voiceDraftMaxTexts caps the texts one draft carries: a corpus for a voice
// is a handful of posts, not an archive.
const voiceDraftMaxTexts = 20

func (o *Orchestrator) voiceDraftAction(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring, ok func(any) daemon.InjectHiring,
	fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	if o.Voices == nil {
		return fail("voices are not configured on this installation")
	}
	name := strings.TrimSpace(req.VoiceName)
	if name == "" {
		return fail("name the voice (\"name\") — it is how a person finds it on the voices page")
	}
	description := strings.TrimSpace(req.Description)
	var texts []daemon.VoiceText
	for _, t := range req.VoiceTexts {
		if strings.TrimSpace(t.Text) != "" {
			texts = append(texts, t)
		}
	}
	switch {
	case description == "" && len(texts) == 0:
		return fail("a voice draft needs a \"description\" in plain words or \"texts\" " +
			"([{\"name\":\"…\",\"text\":\"…\"}]) written in the voice")
	case description != "" && len(texts) > 0:
		return fail("send a description OR texts, not both — a voice is either described or measured; " +
			"texts can be added to a described voice later, on the voices page")
	case len(texts) > voiceDraftMaxTexts:
		return fail("at most %d texts in one draft", voiceDraftMaxTexts)
	}
	source := voice.FromTexts
	if description != "" {
		source = voice.FromDescription
	}
	agentID := agent.ID
	v, err := o.Voices.CreateWith(ctx, agent.OrgID, voice.Draft{Name: name, Language: req.Language,
		Purpose: req.Purpose, Source: source, Description: description, DraftedBy: &agentID})
	switch {
	case errors.Is(err, voice.ErrExists):
		return fail("a voice named %q already exists — pick another name; changing an existing voice "+
			"is done on the voices page", name)
	case errors.Is(err, voice.ErrInvalid):
		return fail("%v", err)
	case err != nil:
		return fail("the draft could not be stored: %v", err)
	}
	// Texts that are not prose are named rather than dropped silently: the
	// agent can say so in the chat, and the person knows what the corpus holds.
	var refused []string
	stored := 0
	for i, t := range texts {
		tname := strings.TrimSpace(t.Name)
		if tname == "" {
			tname = fmt.Sprintf("text-%d", i+1)
		}
		if _, err := o.Voices.AddDocument(ctx, agent.OrgID, v.ID, tname, t.Text, voice.KindAuthor); err != nil {
			refused = append(refused, tname+": "+err.Error())
			continue
		}
		stored++
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]any{"status": "voice_draft", "voice": v.ID.String(), "name": v.Name,
			"source": source, "texts": stored})
	next := "a person writes the card from the description and releases it on the voices page"
	if source == voice.FromTexts {
		next = "a person builds it from the texts and releases the card on the voices page"
	}
	out := map[string]any{"voice_id": v.ID.String(), "name": v.Name, "source": source,
		"texts": stored, "status": "draft", "next": next}
	if len(refused) > 0 {
		out["refused"] = refused
	}
	return ok(out)
}
