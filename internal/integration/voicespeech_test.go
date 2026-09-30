package integration

import (
	"net/http"
	"testing"
)

// TestTheSpokenVoiceFollowsTheChatVoice walks #497's server half: a voice
// gets a spoken voice, refused when the app could not speak it, and the
// conversation tells the app which one an agent speaks with — the chat
// voice the #471 rule chooses, and its speech.
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

	// No voice in the chat slot: nothing to say, the app chooses.
	got := admin.expect(http.MethodGet, conv, nil, http.StatusOK)
	if got["speech"] != nil || got["voice"] != nil || got["level"] != "none" {
		t.Fatalf("without a chat voice: %v", got)
	}
	admin.expect(http.MethodGet, "/api/v1/conversations/"+c["id"].(string)+"/speech?agent="+chatVoice, nil, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/conversations/"+c["id"].(string)+"/speech", nil, http.StatusNotFound)

	base := "/api/v1/voices/" + chatVoice + "/speech"
	admin.expect(http.MethodPut, base, map[string]any{"source": "device", "model": "parakeet"}, http.StatusBadRequest)
	admin.expect(http.MethodPut, base, map[string]any{"source": "device", "model": "piper-en-norman", "speaker": 3}, http.StatusBadRequest)
	admin.expect(http.MethodPut, base, map[string]any{"source": "server"}, http.StatusBadRequest)
	admin.expect(http.MethodPut, base, map[string]any{"source": "server", "model": "kokoro", "rate": 4}, http.StatusBadRequest)
	auditor.expect(http.MethodPut, base, map[string]any{"source": "server", "model": "kokoro"}, http.StatusForbidden)
	srv := admin.expect(http.MethodPut, base, map[string]any{"source": "server", "model": "kokoro", "voice": "af_bella"}, http.StatusOK)
	if sp, _ := srv["speech"].(map[string]any); sp["source"] != "server" || sp["voice"] != "af_bella" {
		t.Fatalf("server speech: %v", srv)
	}
	v := admin.expect(http.MethodPut, base, map[string]any{"source": "device", "model": "piper-en-norman", "rate": 1.2}, http.StatusOK)
	if sp, _ := v["speech"].(map[string]any); sp["model"] != "piper-en-norman" || sp["rate"] != 1.2 {
		t.Fatalf("stored speech: %v", v)
	}

	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voices", map[string]any{"chat": chatVoice}, http.StatusOK)
	got = admin.expect(http.MethodGet, conv, nil, http.StatusOK)
	sp, _ := got["speech"].(map[string]any)
	named, _ := got["voice"].(map[string]any)
	if got["level"] != "agent" || named["name"] != "Kollegial" || sp["source"] != "device" || sp["model"] != "piper-en-norman" {
		t.Fatalf("with the agent's chat voice: %v", got)
	}

	// Cleared, the voice still applies and leaves the sound to the app.
	v = admin.expect(http.MethodPut, base, map[string]any{}, http.StatusOK)
	if v["speech"] != nil {
		t.Fatalf("cleared speech: %v", v)
	}
	got = admin.expect(http.MethodGet, conv, nil, http.StatusOK)
	if got["speech"] != nil || got["level"] != "agent" {
		t.Fatalf("after clearing: %v", got)
	}
}
