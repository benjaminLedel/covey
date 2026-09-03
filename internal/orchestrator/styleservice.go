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
)

// Measuring and restyling text as platform services (spec/06). The first real
// run of the style gate showed why: the writer's sandbox had no Python for the
// skill's scripts, and the post never passed an action the gate could see. A
// meta action needs neither. covey/style_check measures a text against the
// agent's profile with the same numbers the gate uses; covey/style_apply runs
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
	source   string // "agent" | "defaults"
	language string
}

func (o *Orchestrator) styleContextFor(ctx context.Context, agentID uuid.UUID, text, language string) styleContext {
	lang := strings.TrimSpace(language)
	if lang == "" {
		lang = style.DetectLanguage(text)
	}
	profiles := o.styleProfiles(ctx, agentID)
	if p, ok := style.PickProfile(profiles, lang); ok {
		return styleContext{profile: p, prose: o.styleProse(ctx, agentID), source: "agent", language: lang}
	}
	return styleContext{profile: style.DefaultProfile(lang), source: "defaults", language: lang}
}

// styleProse is the voice the model reads while revising: the prose of
// TONE.md when the agent has one. SOUL.md is not repeated here — the agent's
// own runtime already carries it, and a revision is about the text, not the
// role.
func (o *Orchestrator) styleProse(ctx context.Context, agentID uuid.UUID) string {
	cfg, err := o.Registry.CurrentConfig(ctx, agentID)
	if err != nil {
		return ""
	}
	if _, prose, err := style.ParseProfile(cfg.Files["TONE.md"]); err == nil {
		return prose
	}
	return ""
}

// styleCheckAction: covey/style_check {"text": "...", "language": "de|en"}.
func (o *Orchestrator) styleCheckAction(ctx context.Context, agent agents.Agent, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	text := strings.TrimSpace(req.Text)
	if text == "" {
		return fail("style_check needs \"text\": the draft to measure")
	}
	if len(text) > styleTextLimit {
		return fail("style_check: the text is longer than %d characters; measure it in parts", styleTextLimit)
	}
	sc := o.styleContextFor(ctx, agent.ID, text, req.Language)
	report := style.Check(text, &sc.profile)
	return ok(map[string]any{
		"profile":        sc.source,
		"language":       sc.language,
		"words":          report.Metrics.Words,
		"score":          report.Score,
		"summary":        style.Summary(report),
		"findings":       report.Findings,
		"paragraphs":     report.Paragraphs,
		"revision_order": style.RevisionOrder(text, report, req.Material),
		"note": "Revise the named paragraphs against the named metrics and leave the rest alone; " +
			"the finding is the instruction. HIGH findings are what the style gate acts on.",
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
	provider, err := llm.Resolve(ctx, o.Secrets, agent.OrgID)
	if err != nil {
		if errors.Is(err, llm.ErrNoCredential) {
			return fail("style_apply needs a control-plane model: the organisation has no LLM credential configured " +
				"(Setup → model access). Use style_check and revise the named paragraphs yourself.")
		}
		return fail("style_apply: %v", err)
	}
	sc := o.styleContextFor(ctx, agent.ID, text, req.Language)
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
		"action": "covey:style_apply", "provider": provider.Name(), "profile": sc.source, "language": sc.language,
		"iterations": len(res.Iterations), "score_before": res.Before.Score, "score_after": res.Best.Score,
		"stop": res.StopReason, "error": errString(err),
	})
	if err != nil && len(res.Iterations) == 0 {
		return fail("style_apply: %v", err)
	}
	data := map[string]any{
		"text":           res.Text,
		"profile":        sc.source,
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
