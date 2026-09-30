package integration

import (
	"net/http"
	"strings"
	"testing"
)

// TestTheSpokenVoiceFollowsTheChatVoice walks #497's server half: a voice
// gets a spoken voice at the voice provider — a voice name, a style hint,
// a speed — refused when the provider could not take it, and the
// conversation tells the app which one an agent speaks with: the chat voice
// the #471 rule chooses, and its speech.
func TestTheSpokenVoiceFollowsTheChatVoice(t *testing.T) {
	s := newStack(t)
	admin := teamLogin(t, s)
	s.mitglied(t, "auditor@test.local", "Auditor", "auditor", "auditor-passwort")
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")
	agent := s.newSupportAgent("sprecher")
	chatVoice := builtVoice(t, admin, "Kollegial")

	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	conv := "/api/v1/conversations/" + c["id"].(string) + "/speech?agent=" + agent.ID.String()

	// No voice in the chat slot: nothing set, the provider's default voice.
	got := admin.expect(http.MethodGet, conv, nil, http.StatusOK)
	if got["speech"] != nil || got["voice"] != nil || got["level"] != "none" {
		t.Fatalf("without a chat voice: %v", got)
	}
	admin.expect(http.MethodGet, "/api/v1/conversations/"+c["id"].(string)+"/speech?agent="+chatVoice, nil, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/conversations/"+c["id"].(string)+"/speech", nil, http.StatusNotFound)

	base := "/api/v1/voices/" + chatVoice + "/speech"
	admin.expect(http.MethodPut, base, map[string]any{"voice": "alloy", "speed": 2}, http.StatusBadRequest)
	admin.expect(http.MethodPut, base, map[string]any{"voice": strings.Repeat("v", 101)}, http.StatusBadRequest)
	admin.expect(http.MethodPut, base, map[string]any{"instructions": strings.Repeat("i", 301)}, http.StatusBadRequest)
	auditor.expect(http.MethodPut, base, map[string]any{"voice": "alloy"}, http.StatusForbidden)
	v := admin.expect(http.MethodPut, base, map[string]any{"voice": " af_bella ", "instructions": "calm and warm", "speed": 1.2}, http.StatusOK)
	if sp, _ := v["speech"].(map[string]any); sp["voice"] != "af_bella" || sp["instructions"] != "calm and warm" || sp["speed"] != 1.2 {
		t.Fatalf("stored speech: %v", v)
	}

	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voices", map[string]any{"chat": chatVoice}, http.StatusOK)
	got = admin.expect(http.MethodGet, conv, nil, http.StatusOK)
	sp, _ := got["speech"].(map[string]any)
	named, _ := got["voice"].(map[string]any)
	if got["level"] != "agent" || named["name"] != "Kollegial" || sp["voice"] != "af_bella" || got["instructions"] != "calm and warm" {
		t.Fatalf("with the agent's chat voice: %v", got)
	}

	// Cleared, the voice still applies: the provider's default voice, with
	// a style derived from the chat tone.
	v = admin.expect(http.MethodPut, base, map[string]any{}, http.StatusOK)
	if v["speech"] != nil {
		t.Fatalf("cleared speech: %v", v)
	}
	got = admin.expect(http.MethodGet, conv, nil, http.StatusOK)
	if got["speech"] != nil || got["level"] != "agent" || got["instructions"] == "" || got["instructions"] == "calm and warm" {
		t.Fatalf("after clearing: %v", got)
	}
}
