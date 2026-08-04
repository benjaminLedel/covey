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

// Config sync: ACCESS.md and EGRESS.md are the text view onto state that is
// also maintained through the UI (accesses + tool assignment resp. egress
// templates + own hosts). So that text and UI config never diverge, each file
// exists exactly once and both directions write the same store: GET renders
// the files live from the DB, PUT parses and applies them. The config version
// stores the submitted text as a snapshot.
//
// The keywords of both file formats stay German (system:/scope:/tools: with
// the value "alle", templates: with "keine", - host: with notiz:) — they are
// the data format that agents.ParseAccess and parseEgressFile read, and
// existing config versions carry them.

// errNeedsSecurityRole: the principal wants to change something via text edit
// that only security roles may change through the UI (tools, egress).
var errNeedsSecurityRole = errors.New("only platform_admin or security may change tool assignment and egress")

// renderAccessFile builds ACCESS.md from the materialized accesses and the
// tool assignment — one line per system, attributes as read by ParseAccess.
func (s *Server) renderAccessFile(ctx context.Context, agentID uuid.UUID) (string, error) {
	accs, err := s.Registry.Accesses(ctx, agentID)
	if err != nil {
		return "", err
	}
	sort.Slice(accs, func(i, j int) bool { return accs[i].System < accs[j].System })

	var b strings.Builder
	b.WriteString("# Accesses — which target systems this agent may use (references, never secrets).\n")
	b.WriteString("# scope: brokered permissions · tools: tool allowlist of the agent (\"alle\" = no restriction).\n")
	b.WriteString("# In sync with the \"Tools\" tab — changes here take effect there and vice versa.\n\n")
	if len(accs) == 0 {
		b.WriteString("# (no systems — the broker refuses every credential request)\n")
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

// egressSpec is the agent-editable part of EGRESS.md: template names and own
// hosts. Base allowlist/ENV are org-wide and informational only.
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

// renderEgressFile builds EGRESS.md from the agent's egress store.
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
	b.WriteString("# Egress — which hosts this agent may reach outbound; everything else is\n")
	b.WriteString("# blocked fail-closed by the proxy. Maintained here: templates + own hosts;\n")
	b.WriteString("# in sync with the \"Egress\" tab — changes here take effect there and vice versa.\n")
	var basis []string
	for _, h := range defaults {
		basis = append(basis, h.Pattern)
	}
	basis = append(basis, s.EgressDefaults...)
	if len(basis) > 0 {
		fmt.Fprintf(&b, "# Base of the organization (maintained centrally): %s\n", strings.Join(basis, ", "))
	}
	if len(allNames) > 0 {
		fmt.Fprintf(&b, "# Available templates: %s\n", strings.Join(allNames, ", "))
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

// parseEgressFile reads EGRESS.md: one "templates:" line (comma separated,
// "keine" = empty) and "- host:" lines with an optional notiz. Comments (#)
// and everything else are ignored.
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

// prepareConfigApply validates ACCESS.md/EGRESS.md and returns the
// write-through into the UI stores. Without a security role, text edits to
// tools and egress are forbidden (the same RBAC as the tabs) — unchanged files
// may be saved along at any time, though.
func (s *Server) prepareConfigApply(ctx context.Context, orgID, agentID uuid.UUID, files map[string]string, canSecurity bool) (func(context.Context) error, error) {
	// If a file is missing from the request entirely, its area stays untouched —
	// an omitted EGRESS.md means "no change", not "delete everything".
	accessContent, hasAccess := files["ACCESS.md"]
	egressContent, hasEgress := files["EGRESS.md"]

	// Stores that are not wired up (e.g. test setups) → the area drops out.
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

	// Egress: resolve template names before anything is saved.
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
				return nil, fmt.Errorf("EGRESS.md: unknown template %q (available: %s)", name, strings.Join(names, ", "))
			}
			wantTemplates[t.ID] = true
		}
		if cfg, err = s.EgressStore.AgentConfig(ctx, agentID); err != nil {
			return nil, err
		}
	}

	// Change detection for RBAC: what would the write-through switch around?
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
				return fmt.Errorf("tools for %s: %w", a.System, err)
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
