package guardrails

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func rule(scope string, agentID *uuid.UUID, ruleType, pattern string) Rule {
	return Rule{ID: uuid.New(), ScopeLevel: scope, AgentID: agentID,
		RuleType: ruleType, Pattern: pattern, Enabled: true}
}

func TestEvaluateFailClosedPrecedence(t *testing.T) {
	agent := uuid.New()
	rules := []Rule{
		rule("global", nil, RuleRequireApproval, "zammad:reply_external"),
		rule("global", nil, RuleDenyAction, "zammad:reply_external"),
	}
	// Deny wins over RequireApproval, no matter the order.
	if v := Evaluate(rules, agent, "zammad:reply_external"); v.Decision != Deny {
		t.Fatalf("deny must win, got %s", v.Decision)
	}
}

func TestEvaluateWildcard(t *testing.T) {
	agent := uuid.New()
	rules := []Rule{rule("global", nil, RuleDenySystem, "hr:*")}
	if v := Evaluate(rules, agent, "hr:personalakte"); v.Decision != Deny {
		t.Fatalf("wildcard deny does not apply, got %s", v.Decision)
	}
	if v := Evaluate(rules, agent, "zammad"); v.Decision != Allow {
		t.Fatalf("unaffected system must be allowed, got %s", v.Decision)
	}
}

func TestEvaluateAgentScoping(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	rules := []Rule{rule("agent", &a, RuleDenyAction, "zammad:escalate")}
	if v := Evaluate(rules, a, "zammad:escalate"); v.Decision != Deny {
		t.Fatalf("agent-scoped rule must apply to a, got %s", v.Decision)
	}
	if v := Evaluate(rules, b, "zammad:escalate"); v.Decision != Allow {
		t.Fatalf("agent-scoped rule must not hit b, got %s", v.Decision)
	}
}

func TestEvaluateDisabledRule(t *testing.T) {
	r := rule("global", nil, RuleDenyAction, "*")
	r.Enabled = false
	if v := Evaluate([]Rule{r}, uuid.New(), "anything"); v.Decision != Allow {
		t.Fatalf("disabled rule must not apply, got %s", v.Decision)
	}
}

func TestEvaluateRequireApproval(t *testing.T) {
	rules := []Rule{rule("global", nil, RuleRequireApproval, "zammad:reply_external")}
	v := Evaluate(rules, uuid.New(), "zammad:reply_external")
	if v.Decision != RequireApproval {
		t.Fatalf("got %s", v.Decision)
	}
	if v.Rule == nil {
		t.Fatal("triggering rule must come along (recording/alerts)")
	}
}

func TestValidate(t *testing.T) {
	agent := uuid.New()
	valid := func(r Rule) Rule {
		if len(r.Params) == 0 {
			r.Params = json.RawMessage(`{}`)
		}
		return r
	}
	cases := []struct {
		name    string
		rule    Rule
		wantErr bool
	}{
		{"deny global ok", valid(rule("global", nil, RuleDenyAction, "zammad:*")), false},
		{"approval agent ok", valid(rule("agent", &agent, RuleRequireApproval, "zammad:reply_external")), false},
		{"pattern missing", valid(rule("global", nil, RuleDenyAction, "  ")), true},
		{"unknown type", valid(rule("global", nil, "yolo", "*")), true},
		{"unknown scope", valid(rule("planet", nil, RuleDenyAction, "*")), true},
		{"agent scope without agent_id", valid(rule("agent", nil, RuleDenyAction, "*")), true},
		{"global with agent_id", valid(rule("global", &agent, RuleDenyAction, "*")), true},
		{"budget without usd", valid(rule("global", nil, RuleBudgetLimit, "*")), true},
	}
	for _, c := range cases {
		if err := Validate(c.rule); (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
	budget := rule("global", nil, RuleBudgetLimit, "*")
	budget.Params, _ = json.Marshal(map[string]float64{"usd": 5})
	if err := Validate(budget); err != nil {
		t.Errorf("budget with usd>0 must be valid: %v", err)
	}
}

func TestBudgetLimitTightestWins(t *testing.T) {
	agent := uuid.New()
	mk := func(scope string, aid *uuid.UUID, usd float64) Rule {
		r := rule(scope, aid, RuleBudgetLimit, "*")
		r.Params, _ = json.Marshal(map[string]float64{"usd": usd})
		return r
	}
	rules := []Rule{mk("global", nil, 10), mk("agent", &agent, 2.5)}
	if got := BudgetLimit(rules, agent); got != 2.5 {
		t.Fatalf("tightest cap must win, got %v", got)
	}
	if got := BudgetLimit(rules, uuid.New()); got != 10 {
		t.Fatalf("unrelated agent only gets the global cap, got %v", got)
	}
	if got := BudgetLimit(nil, agent); got != 0 {
		t.Fatalf("no rule means no cap, got %v", got)
	}
}

func TestStyleGateRules(t *testing.T) {
	agent := uuid.New()
	other := uuid.New()
	rules := []Rule{
		{ScopeLevel: "global", RuleType: RuleStyleGate, Pattern: "gitlab:comment*", Enabled: true, Params: json.RawMessage(`{"mode":"deny","min_words":40}`)},
		{ScopeLevel: "agent", AgentID: &other, RuleType: RuleStyleGate, Pattern: "*", Enabled: true},
		{ScopeLevel: "global", RuleType: RuleStyleGate, Pattern: "mail:*", Enabled: false},
		{ScopeLevel: "global", RuleType: RuleRequireApproval, Pattern: "*", Enabled: true},
	}
	got := StyleGates(rules, agent, "gitlab:comment_mr")
	if len(got) != 1 || got[0].Pattern != "gitlab:comment*" {
		t.Fatalf("StyleGates: %+v", got)
	}
	p, err := ParseStyleGate(got[0])
	if err != nil || p.Mode != StyleModeDeny || p.MinWords != 40 || p.MaxDenials != 2 {
		t.Fatalf("params: %+v %v", p, err)
	}
	if v := Evaluate(rules, agent, "gitlab:comment_mr"); v.Decision != RequireApproval {
		t.Fatalf("a style gate must not change Evaluate: %v", v.Decision)
	}
	if got := StyleGates(rules, agent, "mail:send"); len(got) != 0 {
		t.Fatalf("disabled rule applied: %+v", got)
	}
	if err := Validate(Rule{ScopeLevel: "global", RuleType: RuleStyleGate, Pattern: "*", Params: json.RawMessage(`{"mode":"loud"}`)}); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if err := Validate(Rule{ScopeLevel: "global", RuleType: RuleStyleGate, Pattern: "*"}); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
}
