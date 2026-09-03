package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/guardrails"
	"covey/internal/observability"
	"covey/internal/style"
)

// The style gate (spec/06): before an action with free text runs, the text is
// measured against the profile in the agent's TONE.md. What a finding does is
// the rule's mode. warn records it. deny hands the findings back to the agent
// as the reason, and the agent revises and retries; that is the revision loop
// of the covey-style skill with the agent's own runtime as the model and no
// second model call in the control plane. After max_denials on the same task
// and action the text goes to the approval gate with the findings attached,
// so a loop that does not converge ends with a human, not with the turn limit.
// approval goes there at once.
//
// A style finding is a measurement, not a security boundary: without a
// profile the gate records that it did not apply and lets the action pass.

const styleReasonLimit = 6000

// styleGate returns a decision when a style_gate rule applies and has
// something to say; nil lets decideAction continue to auto-allow.
func (o *Orchestrator) styleGate(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestApproval, rules []guardrails.Rule) *daemon.ApprovalDecision {

	gates := guardrails.StyleGates(rules, agent.ID, req.Action)
	if len(gates) == 0 {
		return nil
	}
	rule, params := strictestStyleGate(gates)
	text := freeText(req.Params, params.MinWords)
	if text == "" {
		return nil
	}
	record := func(decision string, extra map[string]any) {
		data := map[string]any{"rule": guardrails.RuleStyleGate, "pattern": rule.Pattern,
			"action": req.Action, "decision": decision, "mode": params.Mode}
		for k, v := range extra {
			data[k] = v
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindGuardrail, data)
		o.events.Publish(Event{Type: "guardrail", AgentID: agent.ID.String(), OrgID: agent.OrgID,
			Data: map[string]string{"action": req.Action, "decision": decision, "rule": guardrails.RuleStyleGate}})
	}

	profiles := o.styleProfiles(ctx, agent.ID)
	if len(profiles) == 0 {
		record("skipped", map[string]any{"reason": "no style profile in the agent's config (TONE.md)"})
		return nil
	}
	// A bilingual agent carries one profile per language; the text says which
	// one applies. A text in a language no profile covers is not measured
	// against the wrong bands.
	lang := style.DetectLanguage(text)
	profile, ok := style.PickProfile(profiles, lang)
	if !ok {
		record("skipped", map[string]any{"reason": "no style profile for the text's language", "language": lang})
		return nil
	}
	report := style.Check(text, &profile)
	// The gate acts on HIGH findings only: a metric a full band width outside
	// the author's band, or over an absolute floor. MEDIUM findings and the
	// paragraph pointers travel with the reason as guidance; on their own they
	// would fire on the house's own texts, and a gate that fires on everything
	// is furniture.
	if !style.HasHigh(report) {
		return nil
	}
	found := map[string]any{"score": report.Score, "summary": style.Summary(report),
		"findings": report.Findings, "paragraphs": report.Paragraphs}

	mode := params.Mode
	if mode == guardrails.StyleModeDeny {
		n := o.countStyleDenial(taskID, req.Action)
		if n <= params.MaxDenials {
			found["denial"] = n
			record("denied", found)
			return &daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
				Reason: denialReason(rule.Pattern, report)}
		}
		found["denials"] = n - 1
		mode = guardrails.StyleModeApproval
	}
	if mode == guardrails.StyleModeApproval {
		record("require_approval", found)
		v := o.approvalGate(ctx, agent, taskID, req.Action, withStyleFindings(req.Params, report), "")
		switch {
		case v.Error != "":
			return &daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied", Reason: v.Error}
		case v.Approved:
			return &daemon.ApprovalDecision{RequestID: req.RequestID, Status: "approved"}
		}
		return &daemon.ApprovalDecision{RequestID: req.RequestID, Status: "pending",
			ApprovalID: v.ApprovalID, CorrelationKey: v.CorrelationKey}
	}
	record("warn", found)
	return nil
}

// strictestStyleGate: when several rules match, deny beats approval beats
// warn, and the lower thresholds win.
func strictestStyleGate(gates []guardrails.Rule) (guardrails.Rule, guardrails.StyleGateParams) {
	rank := map[string]int{guardrails.StyleModeWarn: 0, guardrails.StyleModeApproval: 1, guardrails.StyleModeDeny: 2}
	best := gates[0]
	bestP, _ := guardrails.ParseStyleGate(best)
	for _, g := range gates[1:] {
		p, err := guardrails.ParseStyleGate(g)
		if err != nil {
			continue
		}
		if rank[p.Mode] > rank[bestP.Mode] {
			best, bestP = g, p
			continue
		}
		if p.MinWords < bestP.MinWords {
			bestP.MinWords = p.MinWords
		}
		if p.MaxDenials < bestP.MaxDenials {
			bestP.MaxDenials = p.MaxDenials
		}
	}
	return best, bestP
}

// freeText collects the string fields of an action's params that are long
// enough to have a style, joined as paragraphs.
func freeText(params json.RawMessage, minWords int) string {
	if len(params) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(params, &v); err != nil {
		return ""
	}
	var parts []string
	var walk func(x any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			if style.WordCount(t) >= minWords {
				parts = append(parts, strings.TrimSpace(t))
			}
		case map[string]any:
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(v)
	return strings.Join(parts, "\n\n")
}

// styleProfiles collects the profile blocks of the agent's config: TONE.md
// first, then every other Markdown file, in name order.
func (o *Orchestrator) styleProfiles(ctx context.Context, agentID uuid.UUID) []style.Profile {
	cfg, err := o.Registry.CurrentConfig(ctx, agentID)
	if err != nil {
		return nil
	}
	out := style.ParseProfiles(cfg.Files["TONE.md"])
	names := make([]string, 0, len(cfg.Files))
	for name := range cfg.Files {
		if name != "TONE.md" && strings.HasSuffix(name, ".md") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, style.ParseProfiles(cfg.Files[name])...)
	}
	return out
}

// countStyleDenial counts the denials of one task's action and returns the
// new count. The map lives for the process; a task that outlives a restart
// starts the count again, which errs towards letting the agent try.
func (o *Orchestrator) countStyleDenial(taskID uuid.UUID, action string) int {
	o.styleMu.Lock()
	defer o.styleMu.Unlock()
	if o.styleDenials == nil {
		o.styleDenials = map[string]int{}
	}
	key := taskID.String() + "|" + action
	o.styleDenials[key]++
	return o.styleDenials[key]
}

func denialReason(pattern string, r style.Report) string {
	reason := fmt.Sprintf("style gate (%s): %s. Revise the text against these findings, then retry the action. "+
		"Keep every number, name, quote and link; change only the paragraphs named.\n%s",
		pattern, style.Summary(r), style.FindingsText(r))
	if len(reason) > styleReasonLimit {
		reason = reason[:styleReasonLimit] + "\n…"
	}
	return reason
}

// withStyleFindings adds the findings to the params a reviewer sees.
func withStyleFindings(params json.RawMessage, r style.Report) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(params, &m); err != nil || m == nil {
		m = map[string]any{"params": json.RawMessage(params)}
	}
	m["style_findings"] = map[string]any{"score": r.Score, "summary": style.Summary(r),
		"findings": r.Findings, "paragraphs": r.Paragraphs}
	out, err := json.Marshal(m)
	if err != nil {
		return params
	}
	return out
}
