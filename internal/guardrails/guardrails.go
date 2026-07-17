// Package guardrails ist die Policy-Engine (spec/06): zentral definierte,
// plattform-erzwungene Grenzen. Entschieden wird IMMER in der Control Plane
// (der Daemon ist ausführendes Organ), Default ist fail-closed.
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

// Regeltypen. Alle Regeln sind additiv-restriktiv: eine engere Ebene kann
// verschärfen, eine globale Deny-Regel ist nicht aufweichbar.
const (
	RuleDenySystem      = "deny_system"      // Broker: kein Token für dieses System
	RuleDenyAction      = "deny_action"      // Tool-Layer: Aktion hart verboten
	RuleRequireApproval = "require_approval" // Approval-Gate: nur mit Freigabe
	RuleBudgetLimit     = "budget_limit"     // Kosten-Deckel (params: {"usd": N})
)

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

// Verdict ist das Ergebnis einer Policy-Entscheidung inkl. auslösender Regel.
type Verdict struct {
	Decision Decision
	Rule     *Rule
}

// Validate prüft eine Regel vor dem Persistieren — fail-closed heißt auch:
// keine Regeln speichern, die nie greifen oder mehrdeutig wären.
func Validate(r Rule) error {
	switch r.RuleType {
	case RuleDenySystem, RuleDenyAction, RuleRequireApproval:
		if strings.TrimSpace(r.Pattern) == "" {
			return errors.New("pattern ist Pflicht")
		}
	case RuleBudgetLimit:
		var p struct {
			USD float64 `json:"usd"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil || p.USD <= 0 {
			return errors.New("budget_limit braucht params.usd > 0")
		}
	default:
		return errors.New("unbekannter rule_type: " + r.RuleType)
	}
	switch r.ScopeLevel {
	case "global", "team":
		if r.AgentID != nil {
			return errors.New("agent_id nur bei scope_level=agent")
		}
	case "agent":
		if r.AgentID == nil {
			return errors.New("scope_level=agent braucht agent_id")
		}
	default:
		return errors.New("unbekanntes scope_level: " + r.ScopeLevel)
	}
	return nil
}

// matches prüft ein Muster wie "zammad:reply_external", "zammad:*" oder "*"
// gegen einen konkreten Aktions-/System-Bezeichner.
func matches(pattern, subject string) bool {
	if pattern == "*" || pattern == subject {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(subject, prefix)
	}
	return false
}

// Evaluate wendet die Regeln auf ein Subjekt an (z. B. "zammad" für einen
// Credential-Request oder "zammad:reply_external" für eine Aktion).
// Deny schlägt RequireApproval schlägt Allow — fail-closed in der Reihung.
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

// BudgetLimit liefert den engsten anwendbaren Kosten-Deckel (0 = keiner).
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

// --- Persistenz ---

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

// SetEnabled schaltet eine Regel scharf oder pausiert sie — Pausieren statt
// Löschen erhält die Regel-Historie und macht Experimente reversibel.
func (s *Store) SetEnabled(ctx context.Context, orgID, id uuid.UUID, enabled bool) (Rule, error) {
	var r Rule
	err := s.pool.QueryRow(ctx, `UPDATE guardrails SET enabled=$3 WHERE org_id=$1 AND id=$2
		RETURNING id, org_id, scope_level, agent_id, rule_type, pattern, params, enabled, created_at`,
		orgID, id, enabled).Scan(&r.ID, &r.OrgID, &r.ScopeLevel, &r.AgentID, &r.RuleType, &r.Pattern, &r.Params, &r.Enabled, &r.CreatedAt)
	return r, err
}

func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM guardrails WHERE org_id=$1 AND id=$2", orgID, id)
	return err
}
