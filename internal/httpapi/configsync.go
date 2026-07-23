package httpapi

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/egress"
)

// Config-Sync: ACCESS.md und EGRESS.md sind die Text-Sicht auf Zustand, der
// auch über die Oberfläche gepflegt wird (Zugänge + Tool-Zuweisung bzw.
// Egress-Templates + eigene Hosts). Damit Text- und UI-Config nie divergieren,
// gibt es jede Datei genau einmal und beide Richtungen schreiben denselben
// Store: GET rendert die Dateien live aus der DB, PUT parst sie und wendet
// sie an. Die Config-Version speichert den eingereichten Text als Snapshot.

// errNeedsSecurityRole: der Principal will per Text-Edit etwas ändern, das
// über die Oberfläche nur Security-Rollen dürfen (Tools, Egress).
var errNeedsSecurityRole = errors.New("tool-zuweisung und egress ändern nur platform_admin oder security")

// renderAccessFile baut ACCESS.md aus den materialisierten Zugängen und der
// Tool-Zuweisung — eine Zeile pro System, Attribute wie von ParseAccess gelesen.
func (s *Server) renderAccessFile(ctx context.Context, agentID uuid.UUID) (string, error) {
	accs, err := s.Registry.Accesses(ctx, agentID)
	if err != nil {
		return "", err
	}
	sort.Slice(accs, func(i, j int) bool { return accs[i].System < accs[j].System })

	var b strings.Builder
	b.WriteString("# Zugänge — welche Zielsysteme dieser Agent nutzen darf (Referenzen, nie Secrets).\n")
	b.WriteString("# scope: gebrokerte Berechtigungen · tools: Tool-Allowlist des Agenten („alle“ = keine Einschränkung).\n")
	b.WriteString("# Synchron mit dem Reiter „Tools“ — Änderungen hier wirken dort und umgekehrt.\n\n")
	if len(accs) == 0 {
		b.WriteString("# (keine Systeme — der Broker verweigert jede Credential-Anfrage)\n")
	}
	for _, a := range accs {
		fmt.Fprintf(&b, "- system: %s", a.System)
		if len(a.Scopes) > 0 {
			fmt.Fprintf(&b, "   scope: %s", strings.Join(a.Scopes, ","))
		}
		var tools []string
		if s.Targets != nil {
			if tools, err = s.Targets.AgentTools(ctx, agentID, a.System); err != nil {
				return "", err
			}
		}
		if len(tools) > 0 {
			fmt.Fprintf(&b, "   tools: %s", strings.Join(tools, ", "))
		} else {
			b.WriteString("   tools: alle")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// egressSpec ist der agent-editierbare Teil von EGRESS.md: Template-Namen
// und eigene Hosts. Basis-Allowlist/ENV sind org-weit und nur informativ.
type egressSpec struct {
	Templates []string
	Hosts     []hostSpec
}

type hostSpec struct{ Pattern, Note string }

func (e *egressSpec) hasHost(pattern string) bool {
	for _, h := range e.Hosts {
		if h.Pattern == pattern {
			return true
		}
	}
	return false
}

// renderEgressFile baut EGRESS.md aus dem Egress-Store des Agenten.
func (s *Server) renderEgressFile(ctx context.Context, orgID, agentID uuid.UUID) (string, error) {
	cfg, err := s.EgressStore.AgentConfig(ctx, agentID)
	if err != nil {
		return "", err
	}
	templates, err := s.EgressStore.ListTemplates(ctx, orgID)
	if err != nil {
		return "", err
	}
	defaults, err := s.EgressStore.ListDefaultHosts(ctx, orgID)
	if err != nil {
		return "", err
	}

	assigned := map[uuid.UUID]bool{}
	for _, id := range cfg.TemplateIDs {
		assigned[id] = true
	}
	var assignedNames, allNames []string
	for _, t := range templates {
		allNames = append(allNames, t.Name)
		if assigned[t.ID] {
			assignedNames = append(assignedNames, t.Name)
		}
	}

	var b strings.Builder
	b.WriteString("# Egress — welche Hosts dieser Agent ausgehend erreichen darf; alles andere\n")
	b.WriteString("# blockt der Proxy fail-closed. Gepflegt werden hier Templates + eigene Hosts;\n")
	b.WriteString("# synchron mit dem Reiter „Egress“ — Änderungen hier wirken dort und umgekehrt.\n")
	var basis []string
	for _, h := range defaults {
		basis = append(basis, h.Pattern)
	}
	basis = append(basis, s.EgressDefaults...)
	if len(basis) > 0 {
		fmt.Fprintf(&b, "# Basis der Organisation (zentral gepflegt): %s\n", strings.Join(basis, ", "))
	}
	if len(allNames) > 0 {
		fmt.Fprintf(&b, "# Verfügbare Templates: %s\n", strings.Join(allNames, ", "))
	}
	b.WriteString("\n")

	if len(assignedNames) == 0 {
		b.WriteString("templates: keine\n")
	} else {
		fmt.Fprintf(&b, "templates: %s\n", strings.Join(assignedNames, ", "))
		for _, t := range templates {
			if !assigned[t.ID] {
				continue
			}
			var hosts []string
			for _, h := range t.Hosts {
				hosts = append(hosts, h.Pattern)
			}
			fmt.Fprintf(&b, "#   %s → %s\n", t.Name, strings.Join(hosts, ", "))
		}
	}
	b.WriteString("\n")
	for _, h := range cfg.Hosts {
		fmt.Fprintf(&b, "- host: %s", h.Pattern)
		if h.Note != "" {
			fmt.Fprintf(&b, "   notiz: %s", h.Note)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// parseEgressFile liest EGRESS.md: eine "templates:"-Zeile (kommasepariert,
// "keine" = leer) und "- host:"-Zeilen mit optionaler notiz. Kommentare (#)
// und alles Übrige werden ignoriert.
func parseEgressFile(content string) egressSpec {
	var spec egressSpec
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "templates:"); ok {
			for _, name := range strings.Split(rest, ",") {
				name = strings.TrimSpace(name)
				if name == "" || strings.EqualFold(name, "keine") || name == "-" {
					continue
				}
				spec.Templates = append(spec.Templates, name)
			}
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if rest, ok := strings.CutPrefix(line, "host:"); ok {
			var h hostSpec
			if pattern, note, found := strings.Cut(rest, "notiz:"); found {
				h.Pattern, h.Note = strings.TrimSpace(pattern), strings.TrimSpace(note)
			} else {
				h.Pattern = strings.TrimSpace(rest)
			}
			if h.Pattern != "" && !spec.hasHost(h.Pattern) {
				spec.Hosts = append(spec.Hosts, h)
			}
		}
	}
	return spec
}

// prepareConfigApply validiert ACCESS.md/EGRESS.md und liefert den Write-
// Through in die UI-Stores. Ohne Security-Rolle sind Text-Edits an Tools und
// Egress verboten (dieselbe RBAC wie die Reiter) — unveränderte Dateien
// dürfen aber jederzeit mitgespeichert werden.
func (s *Server) prepareConfigApply(ctx context.Context, orgID, agentID uuid.UUID, files map[string]string, canSecurity bool) (func(context.Context) error, error) {
	// Fehlt eine Datei im Request ganz, bleibt ihr Bereich unangetastet —
	// eine ausgelassene EGRESS.md ist „keine Änderung", nicht „alles löschen".
	accessContent, hasAccess := files["ACCESS.md"]
	egressContent, hasEgress := files["EGRESS.md"]

	// Nicht verdrahtete Stores (z. B. Test-Setups) → der Bereich entfällt.
	if s.Targets == nil {
		hasAccess = false
	}
	if s.EgressStore == nil {
		hasEgress = false
	}

	var accs []agents.SystemAccess
	if hasAccess {
		accs = agents.ParseAccess(accessContent)
	}

	// Egress: Template-Namen auflösen, bevor irgendetwas gespeichert wird.
	var spec egressSpec
	var cfg egress.AgentEgress
	wantTemplates := map[uuid.UUID]bool{}
	if hasEgress {
		spec = parseEgressFile(egressContent)
		templates, err := s.EgressStore.ListTemplates(ctx, orgID)
		if err != nil {
			return nil, err
		}
		byName := map[string]egress.Template{}
		var names []string
		for _, t := range templates {
			byName[t.Name] = t
			names = append(names, t.Name)
		}
		for _, name := range spec.Templates {
			t, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("EGRESS.md: unbekanntes Template %q (verfügbar: %s)", name, strings.Join(names, ", "))
			}
			wantTemplates[t.ID] = true
		}
		if cfg, err = s.EgressStore.AgentConfig(ctx, agentID); err != nil {
			return nil, err
		}
	}

	// Änderungs-Erkennung für RBAC: was würde der Write-Through umstellen?
	egressChanged := hasEgress && len(wantTemplates) != len(cfg.TemplateIDs)
	for _, id := range cfg.TemplateIDs {
		if hasEgress && !wantTemplates[id] {
			egressChanged = true
		}
	}
	haveHosts := map[string]string{}
	for _, h := range cfg.Hosts {
		haveHosts[h.Pattern] = h.Note
	}
	if hasEgress && len(spec.Hosts) != len(cfg.Hosts) {
		egressChanged = true
	}
	for _, h := range spec.Hosts {
		if note, ok := haveHosts[h.Pattern]; !ok || note != h.Note {
			egressChanged = true
		}
	}

	toolsChanged := false
	for _, a := range accs {
		current, err := s.Targets.AgentTools(ctx, agentID, a.System)
		if err != nil {
			return nil, err
		}
		if !equalSet(current, a.Tools) {
			toolsChanged = true
		}
	}
	if (egressChanged || toolsChanged) && !canSecurity {
		return nil, errNeedsSecurityRole
	}

	return func(ctx context.Context) error {
		for _, a := range accs {
			if err := s.Targets.SetAgentTools(ctx, agentID, a.System, a.Tools); err != nil {
				return fmt.Errorf("tools für %s: %w", a.System, err)
			}
		}
		if !hasEgress {
			return nil
		}
		have := map[uuid.UUID]bool{}
		for _, id := range cfg.TemplateIDs {
			have[id] = true
		}
		for id := range wantTemplates {
			if !have[id] {
				if err := s.EgressStore.SetAgentTemplate(ctx, agentID, id, true); err != nil {
					return err
				}
			}
		}
		for id := range have {
			if !wantTemplates[id] {
				if err := s.EgressStore.SetAgentTemplate(ctx, agentID, id, false); err != nil {
					return err
				}
			}
		}
		want := map[string]string{}
		for _, h := range spec.Hosts {
			want[h.Pattern] = h.Note
		}
		for _, h := range cfg.Hosts {
			if note, ok := want[h.Pattern]; !ok || note != h.Note {
				if err := s.EgressStore.DeleteAgentHost(ctx, agentID, h.ID); err != nil {
					return err
				}
			}
		}
		for _, h := range spec.Hosts {
			if note, ok := haveHosts[h.Pattern]; ok && note == h.Note {
				continue
			}
			if _, err := s.EgressStore.AddAgentHost(ctx, agentID, h.Pattern, h.Note); err != nil {
				return fmt.Errorf("EGRESS.md: host %s: %w", h.Pattern, err)
			}
		}
		return nil
	}, nil
}

func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}
