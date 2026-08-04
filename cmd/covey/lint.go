package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/config"
	"covey/internal/db"
	"covey/internal/skills"
)

// runConfigLint checks this installation's agent configurations against the
// rules from agents.Lint and reports which agents need a change.
//
// Why a subcommand and not a CI job: after a platform change every installation
// gets the new platform contract automatically (the system prompt is compiled
// at dispatch time), but the agent config stays as a human wrote it. Whoever
// wants to know which of their agents need catching up needs the tool **where
// they are** — not in the pipeline of whoever runs the instance they got the
// binary from.
//
// The lint changes nothing. It reads, judges and describes.
func runConfigLint(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] != "lint" {
		return fmt.Errorf("usage: covey config lint [--json]")
	}
	asJSON := false
	for _, a := range args[1:] {
		switch a {
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("unknown option %q (--json)", a)
		}
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	subjects, err := lintSubjects(ctx, pool)
	if err != nil {
		return err
	}
	var findings []agents.Finding
	for _, s := range subjects {
		findings = append(findings, agents.Lint(s)...)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if findings == nil {
			findings = []agents.Finding{}
		}
		return enc.Encode(findings)
	}
	printLint(subjects, findings)
	if len(findings) > 0 {
		// Exit code 1 makes the lint checkable from an upgrade script without
		// anyone having to parse the output. Findings are not a tool failure.
		os.Exit(1)
	}
	return nil
}

// lintSubjects collects, per agent, the facts the rules need: the current
// config, the self-created board columns and the number of runs aborted at the
// turn limit.
func lintSubjects(ctx context.Context, pool *pgxpool.Pool) ([]agents.Subject, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.id, a.org_id, a.slug, a.max_turns,
		       COALESCE(c.files, '{}'::jsonb)
		FROM agents a
		LEFT JOIN LATERAL (
			SELECT files FROM agent_config_versions v
			WHERE v.agent_id = a.id ORDER BY v.version DESC LIMIT 1
		) c ON TRUE
		ORDER BY a.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// The target per subject, kept by index: the slug is unique only PER
	// organization (migrations/0001: UNIQUE (org_id, slug)), and the lint reads
	// across all organizations. Keyed by slug, one of two agents with the same
	// name would get the other's IDs — and with it a skill resolution that does
	// not belong to it.
	type target struct{ id, orgID uuid.UUID }
	var out []agents.Subject
	var targets []target
	for rows.Next() {
		var (
			id       uuid.UUID
			orgID    uuid.UUID
			s        agents.Subject
			maxTurns *int
			raw      []byte
		)
		if err := rows.Scan(&id, &orgID, &s.Slug, &maxTurns, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &s.Files); err != nil {
			return nil, fmt.Errorf("config of %s: %w", s.Slug, err)
		}
		if maxTurns != nil {
			s.MaxTurns = *maxTurns
		}
		targets = append(targets, target{id: id, orgID: orgID})
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	skillStore := skills.NewStore(pool)
	for i := range out {
		t := targets[i]
		if out[i].AgentStages, err = agentStages(ctx, pool, t.id); err != nil {
			return nil, err
		}
		if out[i].TurnLimitFailures, err = turnLimitFailures(ctx, pool, t.id); err != nil {
			return nil, err
		}
		if out[i].Skills, err = agentSkills(ctx, skillStore, t.orgID, t.id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// agentSkills collects the skills the agent is actually entitled to — its own
// plus linked library skills. Without them the lint would check half a config:
// procedures that moved out of PLAYBOOKS.md into skills would be invisible, and
// rules such as "whoever works, comments" would fire wrongly.
//
// Through the store and not by a query of its own: the precedence rule on name
// collision (the agent's own beats the library) may exist only once, otherwise
// the lint eventually checks something other than what the agent gets.
func agentSkills(ctx context.Context, store *skills.Store, orgID, agentID uuid.UUID) (map[string]string, error) {
	found, err := store.ForAgent(ctx, orgID, agentID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(found))
	for _, sk := range found {
		var b strings.Builder
		for _, f := range sk.Files {
			b.WriteString(f.Content)
			b.WriteString("\n")
		}
		out[sk.Name] = b.String()
	}
	return out, nil
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
// control plane writes the reason into the task's error text. The prefix must
// stay in step with the runtime adapters' message (internal/daemon).
func turnLimitFailures(ctx context.Context, pool *pgxpool.Pool, agentID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM backlog_tasks
		WHERE agent_id=$1 AND error LIKE 'turn limit reached%'`, agentID).Scan(&n)
	return n, err
}

func printLint(subjects []agents.Subject, findings []agents.Finding) {
	byAgent := map[string][]agents.Finding{}
	for _, f := range findings {
		byAgent[f.AgentSlug] = append(byAgent[f.AgentSlug], f)
	}
	slugs := make([]string, 0, len(subjects))
	for _, s := range subjects {
		slugs = append(slugs, s.Slug)
	}
	sort.Strings(slugs)

	affected := 0
	for _, slug := range slugs {
		fs := byAgent[slug]
		if len(fs) == 0 {
			fmt.Printf("%-24s ✓ no findings\n", slug)
			continue
		}
		affected++
		for i, f := range fs {
			mark := "⚠"
			if f.Severity == agents.SeverityInfo {
				mark = "·"
			}
			where := f.File
			if where != "" && f.Line > 0 {
				where = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			name := slug
			if i > 0 {
				name = ""
			}
			fmt.Printf("%-24s %s %s\n", name, mark, strings.TrimSpace(where+" "+f.Rule))
			fmt.Printf("%-24s   %s\n", "", f.Message)
			fmt.Printf("%-24s   → %s\n", "", f.Hint)
		}
	}
	fmt.Println()
	if affected == 0 {
		fmt.Printf("%d agents checked, no findings.\n", len(slugs))
		return
	}
	verb := "need"
	if affected == 1 {
		verb = "needs"
	}
	fmt.Printf("%d of %d agents %s a change.\n", affected, len(slugs), verb)
	fmt.Println("The lint changes nothing — you edit configs in the config tab of the interface")
	fmt.Println("or via POST /api/v1/agents/{id}/config/import (versioned, with history).")
}
