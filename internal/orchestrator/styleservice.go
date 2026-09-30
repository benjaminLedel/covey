package orchestrator

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/llm"
	"covey/internal/observability"
	"covey/internal/style"
	"covey/internal/voice"
)

// Measuring and restyling text as platform services (spec/06). The first real
// run of the style gate showed why: the writer's sandbox had no Python for the
// skill's scripts, and the post never passed an action the gate could see. A
// meta action needs neither. covey/style_check measures a text against the
// profile the gate would use — the voice of the task's outward occasion
// (#471), else the config's TONE.md — with the same numbers; covey/style_apply runs
// the revision loop with the organisation's control-plane model and hands the
// revised text back. Both are recorded as actions like every other.

const (
	styleTextLimit   = 60000 // characters; a blog post is a tenth of that
	styleApplyEffort = "medium"
)

// styleContext is what both services need: the profile that applies to the
// text and the voice around it.
type styleContext struct {
	profile  style.Profile
	prose    string
	source   string // "voice" | "agent" | "defaults"
	language string
	// voice and reason name the voice the task's outward occasion resolved to
	// (#471); empty when no level names one and the config's TONE.md applies.
	voice, reason string
}

// styleContextFor measures a text the way the style gate would measure it
// when it leaves: against the voice the task's outward occasion resolves to
// (#471) — a style_check is the agent asking before it sends — and, when no
// level names a voice, against the config's own TONE.md, as before. The
// other Markdown files of the config come after either.
func (o *Orchestrator) styleContextFor(ctx context.Context, agent agents.Agent, taskID uuid.UUID, text, language string) styleContext {
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = style.DetectLanguage(text)
	}
	var files map[string]string
	if cfg, err := o.Registry.CurrentConfig(ctx, agent.ID); err == nil {
		files = cfg.Files
	}
	sc := styleContext{language: lang, source: "agent"}
	tone := files["TONE.md"]
	if rv := o.outwardVoice(ctx, agent, taskID); rv.voice != nil {
		tone = voice.Render(*rv.voice)
		sc.source, sc.voice, sc.reason = "voice", rv.voice.Name, rv.choice.Reason()
	}
	sc.prose = toneProse(tone)
	profiles := append(style.ParseProfiles(tone), otherProfiles(files)...)
	if p, ok := style.PickProfile(profiles, lang); ok {
		sc.profile = p
		return sc
	}
	sc.profile, sc.source = style.DefaultProfile(lang), "defaults"
	return sc
}

// toneProse is the voice the model reads while revising: the prose of a
// TONE.md. SOUL.md is not repeated here — the agent's own runtime already
// carries it, and a revision is about the text, not the role. A described
// voice (#458) has no profile block, and its TONE.md is prose from top to
// bottom — the card and the passages the revision should follow, also when
// the numbers come from the defaults.
func toneProse(tone string) string {
	if _, prose, err := style.ParseProfile(tone); err == nil {
		return prose
	}
	return strings.TrimSpace(tone)
}

// styleCheckAction: covey/style_check {"text": "...", "language": "de|en"}.
func (o *Orchestrator) styleCheckAction(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	text := strings.TrimSpace(req.Text)
	if text == "" {
		return fail("style_check needs \"text\": the draft to measure")
	}
	if len(text) > styleTextLimit {
		return fail("style_check: the text is longer than %d characters; measure it in parts", styleTextLimit)
	}
	sc := o.styleContextFor(ctx, agent, taskID, text, req.Language)
	report := style.Check(text, &sc.profile)
	// What it was measured against, in the recording beside the proxy's note
	// of the call: "why did the check pass" has an answer months later.
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindAction, map[string]any{
		"action": "covey:style_check", "profile": sc.source, "voice": sc.voice, "voice_reason": sc.reason,
		"language": sc.language, "score": report.Score,
	})
	return ok(map[string]any{
		"profile":        sc.source,
		"voice":          sc.voice,
		"language":       sc.language,
		"words":          report.Metrics.Words,
		"metrics":        report.Metrics.Values,
		"score":          report.Score,
		"summary":        style.Summary(report),
		"findings":       report.Findings,
		"paragraphs":     report.Paragraphs,
		"revision_order": style.RevisionOrder(text, report, req.Material),
		"note": "Revise the named paragraphs against the named metrics and leave the rest alone; " +
			"the finding is the instruction, and where it names phrases, those are the places. " +
			"HIGH findings are what the style gate acts on.",
	})
}

// styleApplyAction: covey/style_apply {"text": "...", "material": "...", "max_iter": 3}.
func (o *Orchestrator) styleApplyAction(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	text := strings.TrimSpace(req.Text)
	if text == "" {
		return fail("style_apply needs \"text\": the draft to revise")
	}
	if len(text) > styleTextLimit {
		return fail("style_apply: the text is longer than %d characters; revise it in parts", styleTextLimit)
	}
	provider, err := llm.Resolve(ctx, o.Secrets, o.Runtimes, agent.OrgID)
	if err != nil {
		if errors.Is(err, llm.ErrNoCredential) {
			return fail("style_apply needs a control-plane model: the organisation has no LLM credential configured " +
				"(Setup → model access). Use style_check and revise the named paragraphs yourself.")
		}
		return fail("style_apply: %v", err)
	}
	sc := o.styleContextFor(ctx, agent, taskID, text, req.Language)
	call := func(ctx context.Context, system, user string) (string, error) {
		return provider.Complete(ctx, llm.Request{
			Tier: llm.TierBest, MaxTokens: 16000, Effort: styleApplyEffort, System: system,
			Messages: []llm.Message{{Role: "user", Content: user}},
		})
	}
	res, err := style.Revise(ctx, style.ReviseInput{
		Text: text, Material: req.Material, Profile: &sc.profile, Prose: sc.prose,
		MaxIter: req.MaxIter, Language: sc.language,
	}, call)
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindAction, map[string]any{
		"action": "covey:style_apply", "provider": provider.Name(), "profile": sc.source, "voice": sc.voice, "voice_reason": sc.reason,
		"language": sc.language, "iterations": len(res.Iterations), "score_before": res.Before.Score, "score_after": res.Best.Score,
		"stop": res.StopReason, "error": errString(err),
	})
	if err != nil && len(res.Iterations) == 0 {
		return fail("style_apply: %v", err)
	}
	data := map[string]any{
		"text":           res.Text,
		"profile":        sc.source,
		"voice":          sc.voice,
		"language":       sc.language,
		"score_before":   res.Before.Score,
		"score_after":    res.Best.Score,
		"summary_before": style.Summary(res.Before),
		"summary_after":  style.Summary(res.Best),
		"iterations":     res.Iterations,
		"stop_reason":    res.StopReason,
		"remaining":      style.FindingsText(res.Best),
	}
	if res.Best.Claims != nil {
		data["claims"] = res.Best.Claims
	}
	if err != nil {
		data["error"] = err.Error()
	}
	return ok(data)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
