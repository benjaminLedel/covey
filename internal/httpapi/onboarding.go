package httpapi

import (
	"net/http"
)

// Onboarding: die ersten Schritte zum ersten arbeitenden Agenten — nicht als
// geklickte Tour, sondern als Checkliste, die den **echten Zustand der
// Organisation** liest.
//
// Der Unterschied ist der Punkt: Eine Tour erzählt, was man tun soll, und
// erzählt es weiter, wenn man es längst getan hat — oder wenn die Oberfläche
// sich geändert hat und die Tour auf einen Knopf zeigt, den es nicht mehr
// gibt. Diese Liste fragt stattdessen die Datenbank: Liegt ein
// Runtime-Credential? Gibt es einen Agenten? Hat er eine SOUL.md? Lag je eine
// Aufgabe an? Ist je ein Lauf gelaufen? Jeder Haken ist damit eine Tatsache,
// keine Behauptung, und die Liste verschwindet von selbst, sobald sie nichts
// mehr zu sagen hat.
//
// Die Schritte sind dieselben wie in der Hilfe („Erste Schritte") — eine
// Reihenfolge, nicht zwei.

// onboardingStep ist ein Schritt samt Erledigt-Status. Der Text steht in der
// Oberfläche (i18n), hier steht nur, was wahr ist.
type onboardingStep struct {
	Key  string `json:"key"`
	Done bool   `json:"done"`
}

type onboardingState struct {
	Steps []onboardingStep `json:"steps"`
	// Done ist true, wenn alle Schritte erledigt sind — dann blendet die
	// Oberfläche die Liste aus, ohne sie durchzählen zu müssen.
	Done bool `json:"done"`
}

// runtimeCredentialKeys sind die Secret-Namen, mit denen die Claude-Code-
// Runtime startet. Ohne einen davon schlägt jede Aufgabe fehl — deshalb ist
// das der erste Schritt und nicht das Anlegen eines Agenten.
var runtimeCredentialKeys = []string{"anthropic_api_key", "claude_code_oauth_token"}

func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)

	var cred, agent, config, task, run bool
	err := s.Pool.QueryRow(r.Context(), `SELECT
		EXISTS (SELECT 1 FROM secrets WHERE org_id=$1 AND key = ANY($2)),
		EXISTS (SELECT 1 FROM agents WHERE org_id=$1),
		EXISTS (SELECT 1 FROM agent_config_versions v JOIN agents a ON a.id = v.agent_id
		        WHERE a.org_id=$1 AND coalesce(v.files->>'SOUL.md', '') <> ''),
		EXISTS (SELECT 1 FROM backlog_tasks WHERE org_id=$1),
		EXISTS (SELECT 1 FROM recording_events WHERE org_id=$1)`,
		p.OrgID, runtimeCredentialKeys).Scan(&cred, &agent, &config, &task, &run)
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
