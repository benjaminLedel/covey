package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"

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

	skillStore := skills.NewStore(pool)
	// uuid.Nil = across all organizations: whoever runs the instance looks at
	// all of it, not at one tenant.
	subjects, err := agents.LintSubjects(ctx, pool, uuid.Nil, func(ctx context.Context, orgID, agentID uuid.UUID) (map[string]string, error) {
		return agentSkills(ctx, skillStore, orgID, agentID)
	})
	if err != nil {
		return err
	}
	var findings []agents.Finding
	for _, s := range subjects {
		findings = append(findings, agents.Lint(s.Subject)...)
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

func printLint(subjects []agents.LintSubject, findings []agents.Finding) {
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
