package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Collecting the facts the lint rules judge used to sit in cmd/covey — so the
// check existed only as a subcommand. That is half a feature: the rule that
// warns about frequent turn-limit aborts would have described the state of
// tester-1 on day one (22 of 23 failures), and nobody would have seen it,
// because nobody runs `covey config lint` on a hunch. Operating tools belong in
// the product, not next to it.
//
// Hence here: the collection is available to the CLI and the HTTP API alike.
// The rules themselves stay a pure function over Subject (lint.go) — this is
// the I/O half.

// SkillLookup delivers an agent's skills (name → contents of its files). A
// parameter rather than a dependency, so the agents package need not know the
// skills package — the caller has the store anyway.
type SkillLookup func(ctx context.Context, orgID, agentID uuid.UUID) (map[string]string, error)

// LintSubject is a Subject together with the agent it belongs to.
type LintSubject struct {
	Subject
	AgentID uuid.UUID
	OrgID   uuid.UUID
}

// LintSubjects collects, per agent, the facts the rules need: the current
// config, the self-created board columns, the number of runs aborted at the
// turn limit and the skills. orgID limits it to one organization; uuid.Nil
// reads across all of them (the CLI's view). skills may be nil — the rules that
// need them are then dropped.
func LintSubjects(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, skills SkillLookup) ([]LintSubject, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.id, a.org_id, a.slug, a.max_turns,
		       COALESCE(c.files, '{}'::jsonb)
		FROM agents a
		LEFT JOIN LATERAL (
			SELECT files FROM agent_config_versions v
			WHERE v.agent_id = a.id ORDER BY v.version DESC LIMIT 1
		) c ON TRUE
		WHERE ($1::uuid IS NULL OR a.org_id = $1)
		ORDER BY a.slug`, uuidOrNil(orgID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LintSubject
	for rows.Next() {
		var (
			ls       LintSubject
			maxTurns *int
			raw      []byte
		)
		if err := rows.Scan(&ls.AgentID, &ls.OrgID, &ls.Slug, &maxTurns, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &ls.Files); err != nil {
			return nil, fmt.Errorf("config of %s: %w", ls.Slug, err)
		}
		if maxTurns != nil {
			ls.MaxTurns = *maxTurns
		}
		out = append(out, ls)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		if out[i].AgentStages, err = agentStages(ctx, pool, out[i].AgentID); err != nil {
			return nil, err
		}
		if out[i].TurnLimitFailures, err = turnLimitFailures(ctx, pool, out[i].AgentID); err != nil {
			return nil, err
		}
		if skills != nil {
			if out[i].Skills, err = skills(ctx, out[i].OrgID, out[i].AgentID); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// LintOrg is the short form for one organization: findings only.
func LintOrg(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, skills SkillLookup) ([]Finding, error) {
	subjects, err := LintSubjects(ctx, pool, orgID, skills)
	if err != nil {
		return nil, err
	}
	findings := []Finding{}
	for _, s := range subjects {
		findings = append(findings, Lint(s.Subject)...)
	}
	return findings, nil
}

// uuidOrNil turns the zero UUID into a SQL NULL — "all organizations".
func uuidOrNil(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func agentStages(ctx context.Context, pool *pgxpool.Pool, agentID uuid.UUID) ([]string, error) {
	rows, err := pool.Query(ctx,
		"SELECT name FROM agent_stages WHERE agent_id=$1 AND created_by='agent' ORDER BY position, created_at", agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// turnLimitFailures counts the runs that were aborted at the turn limit — the
// control plane writes the reason into the task's error text.
//
// Two prefixes, because the message was German before it was English, and the
// tasks of that time are still lying in the backlog. A rule that ignores them
// reports "2 aborts" where there are 22 — and a lint that undercounts is worse
// than none, because the finding stays below its own threshold and never
// appears.
func turnLimitFailures(ctx context.Context, pool *pgxpool.Pool, agentID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM backlog_tasks
		WHERE agent_id=$1 AND (error LIKE 'turn limit reached%' OR error LIKE 'Turn-Limit erreicht%')`,
		agentID).Scan(&n)
	return n, err
}
