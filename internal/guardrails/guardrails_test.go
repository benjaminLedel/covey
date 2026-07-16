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
	// Deny gewinnt gegen RequireApproval, egal in welcher Reihenfolge.
	if v := Evaluate(rules, agent, "zammad:reply_external"); v.Decision != Deny {
		t.Fatalf("Deny muss gewinnen, got %s", v.Decision)
	}
}

func TestEvaluateWildcard(t *testing.T) {
	agent := uuid.New()
	rules := []Rule{rule("global", nil, RuleDenySystem, "hr:*")}
	if v := Evaluate(rules, agent, "hr:personalakte"); v.Decision != Deny {
		t.Fatalf("Wildcard-Deny greift nicht, got %s", v.Decision)
	}
	if v := Evaluate(rules, agent, "zammad"); v.Decision != Allow {
		t.Fatalf("nicht betroffenes System muss allow sein, got %s", v.Decision)
	}
}

func TestEvaluateAgentScoping(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	rules := []Rule{rule("agent", &a, RuleDenyAction, "zammad:escalate")}
	if v := Evaluate(rules, a, "zammad:escalate"); v.Decision != Deny {
		t.Fatalf("agent-gescopte Regel muss für a greifen, got %s", v.Decision)
	}
	if v := Evaluate(rules, b, "zammad:escalate"); v.Decision != Allow {
		t.Fatalf("agent-gescopte Regel darf b nicht treffen, got %s", v.Decision)
	}
}

func TestEvaluateDisabledRule(t *testing.T) {
	r := rule("global", nil, RuleDenyAction, "*")
	r.Enabled = false
	if v := Evaluate([]Rule{r}, uuid.New(), "irgendwas"); v.Decision != Allow {
		t.Fatalf("deaktivierte Regel darf nicht greifen, got %s", v.Decision)
	}
}

func TestEvaluateRequireApproval(t *testing.T) {
	rules := []Rule{rule("global", nil, RuleRequireApproval, "zammad:reply_external")}
	v := Evaluate(rules, uuid.New(), "zammad:reply_external")
	if v.Decision != RequireApproval {
		t.Fatalf("got %s", v.Decision)
	}
	if v.Rule == nil {
		t.Fatal("auslösende Regel muss mitkommen (Recording/Alerts)")
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
		t.Fatalf("engster Deckel muss gewinnen, got %v", got)
	}
	if got := BudgetLimit(rules, uuid.New()); got != 10 {
		t.Fatalf("fremder Agent bekommt nur global, got %v", got)
	}
	if got := BudgetLimit(nil, agent); got != 0 {
		t.Fatalf("ohne Regel kein Deckel, got %v", got)
	}
}
