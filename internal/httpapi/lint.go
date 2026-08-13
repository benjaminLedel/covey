package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
)

// The config lint on the agent page.
//
// It existed as `covey config lint` — and therefore effectively not at all: the
// rule about frequent turn-limit aborts would have described the state of a QA
// agent on day one (22 of 23 failures at the limit, 300 USD burnt without a
// single merge request tested through), and nobody saw it, because nobody runs
// a subcommand on a hunch. Whoever looks at an agent because something is wrong
// with it looks at its page. That is where the finding belongs.
//
// The CLI stays as it is: it looks across all organizations and is the way to
// check an installation after an upgrade without a browser. Both call the same
// rules over the same facts (agents.LintSubjects).

// handleAgentLint: GET /agents/{id}/lint — the findings for this one agent.
func (s *Server) handleAgentLint(w http.ResponseWriter, r *http.Request) {
	agent := agentFrom(r)
	subjects, err := agents.LintSubjects(r.Context(), s.Pool, agent.OrgID, s.lintSkills())
	if err != nil {
		mapErr(w, err)
		return
	}
	findings := []agents.Finding{}
	for _, sub := range subjects {
		if sub.AgentID == agent.ID {
			findings = append(findings, agents.Lint(sub.Subject)...)
		}
	}
	writeJSON(w, http.StatusOK, findings)
}

// lintSkills hands the lint the agent's skills. Without them it would check
// half a config: procedures that moved out of PLAYBOOKS.md into a skill would
// be invisible, and rules such as "whoever works, comments" would fire wrongly
// — a check that nags at good configs gets ignored.
//
// nil when the instance runs without a skill store; the rules that need them
// are then dropped.
func (s *Server) lintSkills() agents.SkillLookup {
	if s.Skills == nil {
		return nil
	}
	return func(ctx context.Context, orgID, agentID uuid.UUID) (map[string]string, error) {
		found, err := s.Skills.ForAgent(ctx, orgID, agentID)
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
}

// handleOrgLint: GET /platform/lint — the same rules over the whole
// organisation, which is the question after an upgrade: which of my agents need
// catching up.
//
// After a platform change every installation gets the new platform contract
// automatically (the system prompt is compiled at dispatch time), but the agent
// config stays as a human wrote it. `covey config lint` answered that at a
// shell; whoever operates this instance through a browser had no way to ask.
type agentFindings struct {
	AgentID  uuid.UUID        `json:"agent_id"`
	Slug     string           `json:"slug"`
	Findings []agents.Finding `json:"findings"`
}

func (s *Server) handleOrgLint(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	subjects, err := agents.LintSubjects(r.Context(), s.Pool, p.OrgID, s.lintSkills())
	if err != nil {
		mapErr(w, err)
		return
	}
	out := []agentFindings{}
	for _, sub := range subjects {
		found := agents.Lint(sub.Subject)
		if len(found) == 0 {
			// Only what needs a change. A list in which everything is fine for
			// most rows is one nobody reads to the end.
			continue
		}
		out = append(out, agentFindings{AgentID: sub.AgentID, Slug: sub.Slug, Findings: found})
	}
	writeJSON(w, http.StatusOK, out)
}
