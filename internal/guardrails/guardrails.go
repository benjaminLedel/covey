// Package guardrails is the policy engine (spec/06): centrally defined,
// platform-enforced limits. The decision is ALWAYS made in the control plane
// (the daemon merely executes it); the default is fail-closed.
package guardrails

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Rule types. All rules are additively restrictive: a narrower level can only
// tighten, and a global deny rule cannot be softened.
const (
	RuleDenySystem      = "deny_system"      // broker: no token for this system
	RuleDenyAction      = "deny_action"      // tool layer: action flatly forbidden
	RuleRequireApproval = "require_approval" // approval gate: only with sign-off
	RuleBudgetLimit     = "budget_limit"     // cost cap (params: {"usd": N})
	RuleStyleGate       = "style_gate"       // text check against TONE.md (params: StyleGateParams)
)

// StyleGateParams configure a style_gate rule. Mode says what a finding does:
// "warn" records it and lets the action pass, "deny" returns the findings to
// the agent as the reason (it revises and retries; after MaxDenials the action
// goes to the approval gate instead), "approval" goes there at once. Texts
// shorter than MinWords are not measured; a one-line comment has no style.
type StyleGateParams struct {
	Mode       string `json:"mode"`
	MinWords   int    `json:"min_words,omitempty"`
	MaxDenials int    `json:"max_denials,omitempty"`
}

const (
	StyleModeWarn     = "warn"
	StyleModeDeny     = "deny"
	StyleModeApproval = "approval"
)

// ParseStyleGate reads a rule's params with the defaults filled in.
func ParseStyleGate(r Rule) (StyleGateParams, error) {
	p := StyleGateParams{Mode: StyleModeWarn, MinWords: 60, MaxDenials: 2}
	if len(r.Params) > 0 && string(r.Params) != "null" {
		var in StyleGateParams
		if err := json.Unmarshal(r.Params, &in); err != nil {
			return p, errors.New("style_gate params: " + err.Error())
		}
		if in.Mode != "" {
			p.Mode = in.Mode
		}
		if in.MinWords > 0 {
			p.MinWords = in.MinWords
		}
		if in.MaxDenials > 0 {
			p.MaxDenials = in.MaxDenials
		}
	}
	switch p.Mode {
	case StyleModeWarn, StyleModeDeny, StyleModeApproval:
	default:
		return p, errors.New("style_gate mode must be warn, deny or approval")
	}
	return p, nil
}

type Decision string

const (
	Allow           Decision = "allow"
	Deny            Decision = "deny"
	RequireApproval Decision = "require_approval"
)

type Rule struct {
	ID         uuid.UUID       `json:"id"`
	OrgID      uuid.UUID       `json:"org_id"`
	ScopeLevel string          `json:"scope_level"` // global | team | agent
	AgentID    *uuid.UUID      `json:"agent_id,omitempty"`
	RuleType   string          `json:"rule_type"`
	Pattern    string          `json:"pattern"`
	Params     json.RawMessage `json:"params"`
	Enabled    bool            `json:"enabled"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Verdict is the outcome of a policy decision including the triggering rule.
type Verdict struct {
	Decision Decision
	Rule     *Rule
}

// ErrNotFound: the rule does not exist — or not in this organisation. Both
// cases get the same answer.
var ErrNotFound = errors.New("guard-rail not found")

// Validate checks a rule before it is persisted — fail-closed also means: do
// not store rules that would never apply or would be ambiguous.
func Validate(r Rule) error {
	switch r.RuleType {
	case RuleDenySystem, RuleDenyAction, RuleRequireApproval:
		if strings.TrimSpace(r.Pattern) == "" {
			return errors.New("pattern is required")
		}
	case RuleBudgetLimit:
		var p struct {
			USD float64 `json:"usd"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil || p.USD <= 0 {
			return errors.New("budget_limit needs params.usd > 0")
		}
	case RuleStyleGate:
		if strings.TrimSpace(r.Pattern) == "" {
			return errors.New("pattern is required")
		}
		if _, err := ParseStyleGate(r); err != nil {
			return err
		}
	default:
		return errors.New("unknown rule_type: " + r.RuleType)
	}
	switch r.ScopeLevel {
	case "global", "team":
		if r.AgentID != nil {
			return errors.New("agent_id only allowed with scope_level=agent")
		}
	case "agent":
		if r.AgentID == nil {
			return errors.New("scope_level=agent needs agent_id")
		}
	default:
		return errors.New("unknown scope_level: " + r.ScopeLevel)
	}
	return nil
}

// matches tests a pattern such as "zammad:reply_external", "zammad:*" or "*"
// against a concrete action/system identifier.
func matches(pattern, subject string) bool {
	if pattern == "*" || pattern == subject {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(subject, prefix)
	}
	return false
}

// Evaluate applies the rules to a subject (e.g. "zammad" for a credential
// request or "zammad:reply_external" for an action). Deny beats
// RequireApproval beats Allow — fail-closed by precedence.
func Evaluate(rules []Rule, agentID uuid.UUID, subject string) Verdict {
	var approval *Rule
	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		if r.ScopeLevel == "agent" && (r.AgentID == nil || *r.AgentID != agentID) {
			continue
		}
		if !matches(r.Pattern, subject) {
			continue
		}
		switch r.RuleType {
		case RuleDenySystem, RuleDenyAction:
			return Verdict{Decision: Deny, Rule: r}
		case RuleRequireApproval:
			approval = r
		}
	}
	if approval != nil {
		return Verdict{Decision: RequireApproval, Rule: approval}
	}
	return Verdict{Decision: Allow}
}

// StyleGates returns the enabled style_gate rules that apply to an agent's
// action, in list order. They do not take part in Evaluate: a style finding is
// not a deny, it is a measurement the caller acts on by the rule's mode.
func StyleGates(rules []Rule, agentID uuid.UUID, subject string) []Rule {
	var out []Rule
	for i := range rules {
		r := &rules[i]
		if !r.Enabled || r.RuleType != RuleStyleGate {
			continue
		}
		if r.ScopeLevel == "agent" && (r.AgentID == nil || *r.AgentID != agentID) {
			continue
		}
		if !matches(r.Pattern, subject) {
			continue
		}
		out = append(out, *r)
	}
	return out
}

// BudgetLimit returns the tightest applicable cost cap (0 = none).
func BudgetLimit(rules []Rule, agentID uuid.UUID) float64 {
	limit := 0.0
	for i := range rules {
		r := &rules[i]
		if !r.Enabled || r.RuleType != RuleBudgetLimit {
			continue
		}
		if r.ScopeLevel == "agent" && (r.AgentID == nil || *r.AgentID != agentID) {
			continue
		}
		var p struct {
			USD float64 `json:"usd"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil || p.USD <= 0 {
			continue
		}
		if limit == 0 || p.USD < limit {
			limit = p.USD
		}
	}
	return limit
}

// --- Persistence ---

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Rule, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, scope_level, agent_id, rule_type, pattern, params, enabled, created_at
		FROM guardrails WHERE org_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.OrgID, &r.ScopeLevel, &r.AgentID, &r.RuleType, &r.Pattern, &r.Params, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, r Rule) (Rule, error) {
	r.ID = uuid.New()
	r.Enabled = true
	if len(r.Params) == 0 {
		r.Params = json.RawMessage(`{}`)
	}
	if err := Validate(r); err != nil {
		return Rule{}, err
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO guardrails (id, org_id, scope_level, agent_id, rule_type, pattern, params, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
		r.ID, r.OrgID, r.ScopeLevel, r.AgentID, r.RuleType, r.Pattern, r.Params, r.Enabled).Scan(&r.CreatedAt)
	return r, err
}

// SetEnabled arms a rule or pauses it — pausing instead of deleting keeps the
// rule history and makes experiments reversible.
func (s *Store) SetEnabled(ctx context.Context, orgID, id uuid.UUID, enabled bool) (Rule, error) {
	var r Rule
	err := s.pool.QueryRow(ctx, `UPDATE guardrails SET enabled=$3 WHERE org_id=$1 AND id=$2
		RETURNING id, org_id, scope_level, agent_id, rule_type, pattern, params, enabled, created_at`,
		orgID, id, enabled).Scan(&r.ID, &r.OrgID, &r.ScopeLevel, &r.AgentID, &r.RuleType, &r.Pattern, &r.Params, &r.Enabled, &r.CreatedAt)
	return r, err
}

func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM guardrails WHERE org_id=$1 AND id=$2", orgID, id)
	if err != nil {
		return err
	}
	// The query is org-scoped, so a foreign rule never matches — yet it used to
	// report success anyway. Anyone trying to delete another organisation's rule
	// now gets "not found" instead of a confirmation for something that never
	// happened.
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
