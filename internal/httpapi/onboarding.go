package httpapi

import (
	"net/http"

	"covey/internal/claudeapi"
)

// Onboarding: the first steps towards the first working agent — not a clicked
// tour, but a checklist that reads the **actual state of the organisation**.
//
// The difference is the point: a tour tells you what to do, and keeps telling
// you long after you have done it — or after the UI has changed and the tour
// points at a button that no longer exists. This list asks the database
// instead: is a runtime credential present? Does an agent exist? Does it have
// a SOUL.md? Was a task ever queued? Did a run ever happen? Every tick is
// therefore a fact, not a claim, and the list disappears by itself once it has
// nothing left to say.
//
// The steps are the same as in the help ("Getting started") — one order, not
// two.

// onboardingStep is a single step plus its done status. The text lives in the
// UI (i18n), here we only record what is true.
type onboardingStep struct {
	Key  string `json:"key"`
	Done bool   `json:"done"`
}

type onboardingState struct {
	Steps []onboardingStep `json:"steps"`
	// Done is true once every step is done — the UI then hides the list
	// without having to count through it.
	Done bool `json:"done"`
}

// The credential step asks only whether the organization holds ANY runtime
// credential — a suffixed name (claude_code_oauth_token_team_a) counts like the
// classic one, and an agent-owned secret counts too, which is why the query
// below deliberately does not filter on agent_id IS NULL. The names themselves
// come from claudeapi.RuntimePrefixes; starts_with rather than LIKE, because
// '_' is a LIKE wildcard and would let anything through.

// The single query deliberately bypasses the store boundary: it asks five
// domains at once (secrets, agents, config, backlog, recording) and cares
// about none of them in substance — only whether anything exists at all. Split
// across five store calls it would be five round trips to the database for a
// view that runs along with every load of the agent overview; parked inside
// one store it would not belong there. This is the job of a BFF: build one
// view out of many sources.
func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)

	var cred, agent, config, task, run bool
	err := s.Pool.QueryRow(r.Context(), `SELECT
		EXISTS (SELECT 1 FROM secrets s, unnest($2::text[]) p
		        WHERE s.org_id=$1 AND (s.key = p OR starts_with(s.key, p || '_'))),
		EXISTS (SELECT 1 FROM agents WHERE org_id=$1),
		EXISTS (SELECT 1 FROM agent_config_versions v JOIN agents a ON a.id = v.agent_id
		        WHERE a.org_id=$1 AND coalesce(v.files->>'SOUL.md', '') <> ''),
		EXISTS (SELECT 1 FROM backlog_tasks WHERE org_id=$1),
		EXISTS (SELECT 1 FROM recording_events WHERE org_id=$1)`,
		p.OrgID, claudeapi.RuntimePrefixes).Scan(&cred, &agent, &config, &task, &run)
	if err != nil {
		mapErr(w, err)
		return
	}

	state := onboardingState{Steps: []onboardingStep{
		{Key: "credential", Done: cred},
		{Key: "agent", Done: agent},
		{Key: "config", Done: config},
		{Key: "task", Done: task},
		{Key: "run", Done: run},
	}}
	state.Done = true
	for _, st := range state.Steps {
		if !st.Done {
			state.Done = false
			break
		}
	}
	writeJSON(w, http.StatusOK, state)
}
