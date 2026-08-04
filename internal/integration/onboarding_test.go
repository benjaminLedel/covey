package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestOnboardingChecklist checks the "first steps" list: every tick comes from
// the organization's real state, not from progress the user interface
// remembers. And it counts only for one's own organization.
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

	// Fresh organization: nothing done.
	for key, done := range steps(admin) {
		if done {
			t.Fatalf("fresh organization: %q is already done", key)
		}
	}

	// Runtime credential.
	admin.expect(http.MethodPut, "/api/v1/secrets/anthropic_api_key",
		map[string]string{"value": "sk-ant-api03-test"}, http.StatusOK)
	if got := steps(admin); !got["credential"] || got["agent"] {
		t.Fatalf("after the secret: %v", got)
	}

	// Agent, config, task — every step ticks exactly its own item.
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "erstschritt", "display_name": "Erster", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	if got := steps(admin); !got["agent"] || got["config"] {
		t.Fatalf("after the agent: %v", got)
	}

	// A config without content in SOUL.md does not count — otherwise the step
	// ticked as soon as anything at all had been saved.
	admin.expect(http.MethodPut, "/api/v1/agents/"+agentID+"/config",
		map[string]any{"files": map[string]string{"SOUL.md": ""}}, http.StatusOK)
	if got := steps(admin); got["config"] {
		t.Fatalf("an empty SOUL.md must not count as done: %v", got)
	}
	admin.expect(http.MethodPut, "/api/v1/agents/"+agentID+"/config",
		map[string]any{"files": map[string]string{"SOUL.md": "# Role\n\nSupport."}}, http.StatusOK)
	if got := steps(admin); !got["config"] || got["task"] {
		t.Fatalf("after the config: %v", got)
	}

	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/tasks",
		map[string]any{"title": "First task", "body": "Test"}, http.StatusCreated)
	if got := steps(admin); !got["task"] || got["run"] {
		t.Fatalf("after the task: %v", got)
	}

	// One run: the recording is the proof that the sandbox actually ran.
	agent, err := s.registry.GetBySlug(ctx, s.orgID, "erstschritt")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.obs.Record(ctx, s.orgID, agent.ID, nil, "lifecycle", map[string]any{"status": "working"}); err != nil {
		t.Fatal(err)
	}
	got := steps(admin)
	if !got["run"] || !got["__done"] {
		t.Fatalf("after the run everything has to be done: %v", got)
	}

	// Another organization, another list: one's progress must not tick the
	// other's items.
	admin.expect(http.MethodPost, "/api/v1/orgs", map[string]any{
		"name": "Zweit-Org", "admin_email": "zweit@test.local",
		"admin_name": "Zweit-Admin", "admin_password": "zweit-passwort",
	}, http.StatusCreated)
	zweit := login(t, s, "zweit@test.local", "zweit-passwort")
	for key, done := range steps(zweit) {
		if done {
			t.Fatalf("second organization: %q must not be done (%v)", key, steps(zweit))
		}
	}
}
