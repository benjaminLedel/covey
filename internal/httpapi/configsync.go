package httpapi

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Generierte Config-Dateien: die über die Oberfläche gepflegten Teile der
// Agenten-Config (Tool-Zuweisung, Egress-Allowlist) als Text-Dateien im Stil
// von ACCESS.md. Sie werden bei jedem Lesen live aus den UI-Stores berechnet —
// Text-Config und UI-Config sind damit per Konstruktion synchron. Als
// speicherbare Dateien sind die Namen reserviert (agents.GeneratedFiles):
// Tools und Egress ändern nur Security-Rollen über ihre Reiter, Config-
// Versionen darf auch der Agent-Owner schreiben.

// generatedConfigFiles berechnet TOOLS.md und EGRESS.md für einen Agenten.
func (s *Server) generatedConfigFiles(ctx context.Context, orgID, agentID uuid.UUID) (map[string]string, error) {
	out := map[string]string{}

	tools, err := s.generatedToolsFile(ctx, orgID, agentID)
	if err != nil {
		return nil, fmt.Errorf("TOOLS.md generieren: %w", err)
	}
	out["TOOLS.md"] = tools

	eg, err := s.generatedEgressFile(ctx, orgID, agentID)
	if err != nil {
		return nil, fmt.Errorf("EGRESS.md generieren: %w", err)
	}
	out["EGRESS.md"] = eg
	return out, nil
}

// generatedToolsFile listet pro aktiviertem Zielsystem die Tool-Zuweisung des
// Agenten — leere Allowlist heißt „alle Tools erlaubt" (wie AgentToolAllowed).
func (s *Server) generatedToolsFile(ctx context.Context, orgID, agentID uuid.UUID) (string, error) {
	var b strings.Builder
	b.WriteString("# Tools — generiert aus der Oberfläche (Reiter „Tools“)\n")
	b.WriteString("# Zuweisung ändern Security-Rollen dort; diese Datei ist immer der Live-Stand.\n\n")

	plugins, err := s.Targets.List(ctx, orgID)
	if err != nil {
		return "", err
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	n := 0
	for _, p := range plugins {
		if !p.Enabled {
			continue
		}
		n++
		if p.Kind != "mcp" {
			fmt.Fprintf(&b, "- system: %s   tools: alle\n", p.Name)
			continue
		}
		allowed, err := s.Targets.AgentTools(ctx, agentID, p.Name)
		if err != nil {
			return "", err
		}
		if len(allowed) == 0 {
			fmt.Fprintf(&b, "- system: %s   tools: alle\n", p.Name)
		} else {
			fmt.Fprintf(&b, "- system: %s   tools: %s\n", p.Name, strings.Join(allowed, ", "))
		}
	}
	if n == 0 {
		b.WriteString("# (keine Zielsysteme aktiviert)\n")
	}
	return b.String(), nil
}

// generatedEgressFile listet die effektive Egress-Allowlist des Agenten mit
// Quelle je Muster — dieselbe Auflösung wie der Egress-Reiter: Basis-Allowlist
// der Org, ENV-Zusätze, zugewiesene Templates, eigene Hosts (dedupliziert).
func (s *Server) generatedEgressFile(ctx context.Context, orgID, agentID uuid.UUID) (string, error) {
	var b strings.Builder
	b.WriteString("# Egress-Allowlist — generiert aus der Oberfläche (Reiter „Egress“)\n")
	b.WriteString("# Alles andere blockt der Egress-Proxy fail-closed.\n\n")

	type entry struct{ pattern, source string }
	var list []entry
	seen := map[string]bool{}
	add := func(pattern, source string) {
		if !seen[pattern] {
			seen[pattern] = true
			list = append(list, entry{pattern, source})
		}
	}

	defaults, err := s.EgressStore.ListDefaultHosts(ctx, orgID)
	if err != nil {
		return "", err
	}
	for _, h := range defaults {
		add(h.Pattern, "Basis")
	}
	for _, p := range s.EgressDefaults {
		add(p, "ENV")
	}

	cfg, err := s.EgressStore.AgentConfig(ctx, agentID)
	if err != nil {
		return "", err
	}
	assigned := map[uuid.UUID]bool{}
	for _, id := range cfg.TemplateIDs {
		assigned[id] = true
	}
	templates, err := s.EgressStore.ListTemplates(ctx, orgID)
	if err != nil {
		return "", err
	}
	for _, t := range templates {
		if !assigned[t.ID] {
			continue
		}
		for _, h := range t.Hosts {
			add(h.Pattern, t.Name)
		}
	}
	for _, h := range cfg.Hosts {
		add(h.Pattern, "eigener Host")
	}

	if len(list) == 0 {
		b.WriteString("# (leer — jede ausgehende Verbindung wird blockiert)\n")
	}
	for _, e := range list {
		fmt.Fprintf(&b, "- host: %s   quelle: %s\n", e.pattern, e.source)
	}
	return b.String(), nil
}
