package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/llm"
)

// TestChatToneReachesTheTriage walks the tone half of #457: the organisation
// sets a default, a voice sets its own, and the triage of an agent carrying
// the voice reads the voice's values where it has them and the organisation's
// where it does not. A value that is not one of the choices is refused.
func TestChatToneReachesTheTriage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("tonfall")
	s.ohneLaeufe(agent.ID)
	modell := &antwortendesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)

	org := admin.expect(http.MethodPatch, "/api/v1/org/chat-tone",
		map[string]any{"address": "sie", "tone": "formal", "emoji": "never", "note": "Wir duzen Kunden nie."}, http.StatusOK)
	if org["address"] != "sie" {
		t.Fatalf("org tone: %v", org)
	}
	admin.expect(http.MethodPatch, "/api/v1/org/chat-tone", map[string]any{"tone": "rude"}, http.StatusBadRequest)
	if got := admin.expect(http.MethodGet, "/api/v1/org/chat-tone", nil, http.StatusOK); got["tone"] != "formal" {
		t.Fatalf("a refused value must not be stored: %v", got)
	}

	v := admin.expect(http.MethodPost, "/api/v1/voices", map[string]any{"name": "Team", "language": "de"}, http.StatusCreated)
	id := v["id"].(string)
	gesetzt := admin.expect(http.MethodPut, "/api/v1/voices/"+id+"/chat-tone",
		map[string]any{"address": "auto", "emoji": "freely"}, http.StatusOK)
	if ton, _ := gesetzt["chat_tone"].(map[string]any); ton["emoji"] != "freely" {
		t.Fatalf("voice tone: %v", gesetzt)
	}
	admin.expect(http.MethodPut, "/api/v1/voices/"+id+"/chat-tone", map[string]any{"emoji": "always"}, http.StatusBadRequest)
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET voice_id=$2 WHERE id=$1`, agent.ID, id); err != nil {
		t.Fatal(err)
	}

	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages",
		map[string]any{"text": "Guten Morgen!"}, http.StatusAccepted)
	wartenAuf(t, "the triage answered", func() bool {
		return strings.Contains(strings.Join(eintraege(t, admin, agent.ID), "\n"), "Gern, mach ich.")
	})
	p := modell.prompts()[0]
	for _, will := range []string{"How you talk in the team chat", "the way they address you", "polite and formal", "emoji are welcome", "Wir duzen Kunden nie."} {
		if !strings.Contains(p, will) {
			t.Errorf("the triage prompt lacks %q:\n%s", will, p)
		}
	}
	if strings.Contains(p, "no emoji at all") {
		t.Error("the voice's emoji setting must win over the organisation's")
	}
}
