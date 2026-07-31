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

// runConfigLint prüft die Agenten-Konfigurationen dieser Installation gegen die
// Regeln aus agents.Lint und meldet, welche Agenten eine Änderung brauchen.
//
// Warum ein Subcommand und keine CI-Aufgabe: Nach einer Plattform-Änderung
// bekommt jede Installation den neuen Plattform-Vertrag automatisch (der
// System-Prompt wird zur Dispatch-Zeit kompiliert), aber die Agenten-Config
// bleibt, wie ein Mensch sie geschrieben hat. Wer wissen will, welche seiner
// Agenten nachgezogen werden müssen, braucht das Werkzeug **bei sich** — nicht
// in der Pipeline dessen, der die Instanz betreibt, von der er das Binary hat.
//
// Der Lint ändert nichts. Er liest, bewertet und beschreibt.
func runConfigLint(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] != "lint" {
		return fmt.Errorf("nutzung: covey config lint [--json]")
	}
	asJSON := false
	for _, a := range args[1:] {
		switch a {
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("unbekannte option %q (--json)", a)
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
		// Exit-Code 1 macht den Lint in einem Upgrade-Skript prüfbar, ohne dass
		// jemand die Ausgabe parsen muss. Befunde sind kein Fehler des Werkzeugs.
		os.Exit(1)
	}
	return nil
}

// lintSubjects sammelt je Agent die Fakten, die die Regeln brauchen: die
// aktuelle Config, die selbst angelegten Board-Spalten und die Zahl der am
// Turn-Limit abgebrochenen Läufe.
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

	var out []agents.Subject
	ids := map[string]uuid.UUID{}
	orgs := map[string]uuid.UUID{}
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
			return nil, fmt.Errorf("config von %s: %w", s.Slug, err)
		}
		if maxTurns != nil {
			s.MaxTurns = *maxTurns
		}
		ids[s.Slug] = id
		orgs[s.Slug] = orgID
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	skillStore := skills.NewStore(pool)
	for i := range out {
		id := ids[out[i].Slug]
		if out[i].AgentStages, err = agentStages(ctx, pool, id); err != nil {
			return nil, err
		}
		if out[i].TurnLimitFailures, err = turnLimitFailures(ctx, pool, id); err != nil {
			return nil, err
		}
		if out[i].Skills, err = agentSkills(ctx, skillStore, orgs[out[i].Slug], id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// agentSkills sammelt die Fähigkeiten, die dem Agenten tatsächlich zustehen —
// eigene plus verlinkte Bibliotheks-Skills. Ohne sie prüfte der Lint eine halbe
// Config: Prozeduren, die aus PLAYBOOKS.md in Skills gewandert sind, wären
// unsichtbar, und Regeln wie „wer arbeitet, kommentiert" schlügen fälschlich an.
//
// Über den Store und nicht per eigener Abfrage: Die Vorrang-Regel bei
// Namensgleichheit (agent-eigen schlägt Bibliothek) darf es nur einmal geben,
// sonst prüft der Lint irgendwann etwas anderes, als der Agent bekommt.
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

// turnLimitFailures zählt die Läufe, die am Turn-Limit abgebrochen sind — die
// Control Plane schreibt den Grund in den Fehlertext der Aufgabe.
func turnLimitFailures(ctx context.Context, pool *pgxpool.Pool, agentID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM backlog_tasks
		WHERE agent_id=$1 AND error LIKE 'Turn-Limit%'`, agentID).Scan(&n)
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

	betroffen := 0
	for _, slug := range slugs {
		fs := byAgent[slug]
		if len(fs) == 0 {
			fmt.Printf("%-24s ✓ keine Befunde\n", slug)
			continue
		}
		betroffen++
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
	if betroffen == 0 {
		fmt.Printf("%d Agenten geprüft, keine Befunde.\n", len(slugs))
		return
	}
	verb := "brauchen"
	if betroffen == 1 {
		verb = "braucht"
	}
	fmt.Printf("%d von %d Agenten %s eine Änderung.\n", betroffen, len(slugs), verb)
	fmt.Println("Der Lint ändert nichts — Configs bearbeitest du im Config-Tab der Oberfläche")
	fmt.Println("oder per POST /api/v1/agents/{id}/config/import (versioniert, mit Historie).")
}
