package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestOnboardingChecklist prüft die „Erste Schritte"-Liste: Jeder Haken kommt
// aus dem echten Zustand der Organisation, nicht aus einem Fortschritt, den
// sich die Oberfläche merkt. Und er zählt nur für die eigene Organisation.
func TestOnboardingChecklist(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	steps := func(c *apiClient) map[string]bool {
		t.Helper()
		var state struct {
			Steps []struct {
				Key  string `json:"key"`
				Done bool   `json:"done"`
			} `json:"steps"`
			Done bool `json:"done"`
		}
		resp := c.do(http.MethodGet, "/api/v1/onboarding", nil)
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		out := map[string]bool{"__done": state.Done}
		for _, st := range state.Steps {
			out[st.Key] = st.Done
		}
		return out
	}

	// Frische Organisation: nichts erledigt.
	for key, done := range steps(admin) {
		if done {
			t.Fatalf("frische organisation: %q ist bereits erledigt", key)
		}
	}

	// Runtime-Credential.
	admin.expect(http.MethodPut, "/api/v1/secrets/anthropic_api_key",
		map[string]string{"value": "sk-ant-api03-test"}, http.StatusOK)
	if got := steps(admin); !got["credential"] || got["agent"] {
		t.Fatalf("nach dem secret: %v", got)
	}

	// Agent, Config, Aufgabe — jeder Schritt hakt genau seinen eigenen Punkt ab.
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "erstschritt", "display_name": "Erster", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	if got := steps(admin); !got["agent"] || got["config"] {
		t.Fatalf("nach dem agenten: %v", got)
	}

	// Eine Config ohne Inhalt in SOUL.md zählt nicht — sonst hakte der Schritt
	// ab, sobald irgendetwas gespeichert wurde.
	admin.expect(http.MethodPut, "/api/v1/agents/"+agentID+"/config",
		map[string]any{"files": map[string]string{"SOUL.md": ""}}, http.StatusOK)
	if got := steps(admin); got["config"] {
		t.Fatalf("leere SOUL.md darf nicht als erledigt zählen: %v", got)
	}
	admin.expect(http.MethodPut, "/api/v1/agents/"+agentID+"/config",
		map[string]any{"files": map[string]string{"SOUL.md": "# Rolle\n\nSupport."}}, http.StatusOK)
	if got := steps(admin); !got["config"] || got["task"] {
		t.Fatalf("nach der config: %v", got)
	}

	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/tasks",
		map[string]any{"title": "Erste Aufgabe", "body": "Test"}, http.StatusCreated)
	if got := steps(admin); !got["task"] || got["run"] {
		t.Fatalf("nach der aufgabe: %v", got)
	}

	// Ein Lauf: das Recording ist der Beleg, dass die Sandbox tatsächlich lief.
	agent, err := s.registry.GetBySlug(ctx, s.orgID, "erstschritt")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.obs.Record(ctx, s.orgID, agent.ID, nil, "lifecycle", map[string]any{"status": "working"}); err != nil {
		t.Fatal(err)
	}
	got := steps(admin)
	if !got["run"] || !got["__done"] {
		t.Fatalf("nach dem lauf muss alles erledigt sein: %v", got)
	}

	// Andere Organisation, andere Liste: der Fortschritt der einen darf die
	// andere nicht abhaken.
	admin.expect(http.MethodPost, "/api/v1/orgs", map[string]any{
		"name": "Zweit-Org", "admin_email": "zweit@test.local",
		"admin_name": "Zweit-Admin", "admin_password": "zweit-passwort",
	}, http.StatusCreated)
	zweit := login(t, s, "zweit@test.local", "zweit-passwort")
	for key, done := range steps(zweit) {
		if done {
			t.Fatalf("zweite organisation: %q darf nicht erledigt sein (%v)", key, steps(zweit))
		}
	}
}
