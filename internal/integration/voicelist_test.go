package integration

import (
	"net/http"
	"testing"
)

// educaVoiceList is educa AI's answer at GET /api/tts/voices: 16 voices in
// five languages and a default.
const educaVoiceList = `{"default":{"language":"de","name":"thorsten"},"fetched_at":"2026-09-30T10:00:00Z","voices":[
{"display_name":"Bernd","language":"de","name":"bernd"},{"display_name":"Erika","language":"de","name":"erika"},
{"display_name":"Eva","language":"de","name":"eva"},{"display_name":"Friedrich","language":"de","name":"friedrich"},
{"display_name":"Karlsson","language":"de","name":"karlsson"},{"display_name":"Katja","language":"de","name":"katja"},
{"display_name":"Thorsten","language":"de","name":"thorsten"},{"display_name":"Carl","language":"en","name":"carl"},
{"display_name":"Linda","language":"en","name":"linda"},{"display_name":"Gabriel","language":"fr","name":"gabriel"},
{"display_name":"Louise","language":"fr","name":"louise"},{"display_name":"Ambre","language":"fr","name":"ambre"},
{"display_name":"Matteo","language":"it","name":"matteo"},{"display_name":"Aurora","language":"it","name":"aurora"},
{"display_name":"Hugo","language":"es","name":"hugo"},{"display_name":"Martina","language":"es","name":"martina"}]}`

// TestAgentsGetTheirOwnSpokenVoice walks #518: the provider's named voices
// are read with the synthesis credentials and offered to every member,
// kept for an hour and read anew when the settings change; an agent whose
// covey voice names no spoken voice gets one assigned from the list for the
// call's language — a different one for a second agent — and /speech hands
// it to the app as speech.voice, the voice its replies, greeting, fillers
// and goodbye are all synthesised with. A covey voice's own spoken voice
// wins; a provider without a list leaves the free text and the default.
func TestAgentsGetTheirOwnSpokenVoice(t *testing.T) {
	const educaKey = "educa-seat-geheim-0815"
	educa, own := newFakeVoiceProvider(t), newFakeVoiceProvider(t)
	educa.voiceList = educaVoiceList
	t.Setenv("COVEY_EDUCA_BASE_URL", educa.URL)

	s := newStack(t)
	admin := teamLogin(t, s)
	s.mitglied(t, "ada@test.local", "Ada", "controlling", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	list := "/api/v1/org/voice-provider/voices"

	// Without a provider: no list.
	if m := ada.expect(http.MethodGet, list, nil, http.StatusOK); m["listed"] != false || len(m["voices"].([]any)) != 0 {
		t.Fatalf("without a provider: %v", m)
	}

	admin.expect(http.MethodPut, "/api/v1/secrets/educa_seat_token", map[string]string{"value": educaKey}, http.StatusOK)
	m := ada.expect(http.MethodGet, list, nil, http.StatusOK)
	voices, _ := m["voices"].([]any)
	def, _ := m["default"].(map[string]any)
	if m["listed"] != true || len(voices) != 16 || def["name"] != "thorsten" || def["language"] != "de" {
		t.Fatalf("educa's list: %v", m)
	}
	if first := voices[0].(map[string]any); first["name"] != "bernd" || first["display_name"] != "Bernd" || first["language"] != "de" {
		t.Fatalf("first voice: %v", first)
	}
	if educa.lastAuth() != "Bearer "+educaKey {
		t.Fatalf("list authorization = %q", educa.lastAuth())
	}
	ada.expect(http.MethodGet, list, nil, http.StatusOK)
	educa.mu.Lock()
	reads := educa.listReads
	educa.mu.Unlock()
	if reads != 1 {
		t.Fatalf("the list was read %d times, want once (cached)", reads)
	}

	speechOf := func(slug string) (map[string]any, string) {
		t.Helper()
		agent := s.newSupportAgent(slug)
		c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
			"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
		return admin.expect(http.MethodGet, "/api/v1/conversations/"+c["id"].(string)+"/speech?agent="+agent.ID.String()+"&lang=de-DE",
			nil, http.StatusOK), agent.ID.String()
	}
	first, firstID := speechOf("sprecher-eins")
	second, _ := speechOf("sprecher-zwei")
	voiceOf := func(m map[string]any) string {
		sp, _ := m["speech"].(map[string]any)
		name, _ := sp["voice"].(string)
		return name
	}
	for _, got := range []map[string]any{first, second} {
		sv, _ := got["spoken_voice"].(map[string]any)
		if got["voice_source"] != "assigned" || voiceOf(got) == "" || sv["language"] != "de" || sv["name"] != voiceOf(got) || got["instructions"] == "" {
			t.Fatalf("assigned voice: %v", got)
		}
	}
	if voiceOf(first) == voiceOf(second) {
		t.Fatalf("two agents speak with the same voice %q", voiceOf(first))
	}
	// Asked again, the same one; the agent's settings say the same.
	again, _ := admin.expect(http.MethodGet, "/api/v1/agents/"+firstID+"/spoken-voice?lang=de", nil, http.StatusOK)["spoken"].(map[string]any)
	if again["name"] != voiceOf(first) || again["source"] != "assigned" {
		t.Fatalf("the agent's settings: %v, the call: %v", again, first)
	}
	// In English, one of the English voices.
	en, _ := admin.expect(http.MethodGet, "/api/v1/agents/"+firstID+"/spoken-voice?lang=en", nil, http.StatusOK)["spoken"].(map[string]any)
	if en["language"] != "en" || (en["name"] != "carl" && en["name"] != "linda") {
		t.Fatalf("English: %v", en)
	}
	// The app synthesises with what /speech named: the fake gets that voice.
	admin.expect(http.MethodPost, "/api/v1/speech/synthesize", map[string]any{"text": "Einen Moment.", "voice": voiceOf(first), "language": "de-DE"}, http.StatusOK)
	if got := educa.lastSpeech()["voice"]; got != voiceOf(first) {
		t.Fatalf("synthesised with %v, want %s", got, voiceOf(first))
	}

	// A covey voice's own spoken voice wins over the assignment.
	agent := s.newSupportAgent("sprecher-drei")
	chatVoice := builtVoice(t, admin, "Kollegial")
	admin.expect(http.MethodPut, "/api/v1/voices/"+chatVoice+"/speech", map[string]any{"voice": "katja", "speed": 1.1}, http.StatusOK)
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voices", map[string]any{"chat": chatVoice}, http.StatusOK)
	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	got := admin.expect(http.MethodGet, "/api/v1/conversations/"+c["id"].(string)+"/speech?agent="+agent.ID.String(), nil, http.StatusOK)
	sp, _ := got["speech"].(map[string]any)
	sv, _ := got["spoken_voice"].(map[string]any)
	if got["voice_source"] != "voice" || sp["voice"] != "katja" || sp["speed"] != 1.1 || sv["display_name"] != "Katja" {
		t.Fatalf("the covey voice's own: %v", got)
	}

	// Another provider: the list is read anew — this one has none, so the
	// settings keep free text and the call speaks the configured default.
	admin.expect(http.MethodPatch, "/api/v1/org/voice-provider", map[string]any{"base_url": own.URL, "voice": "af_bella", "key": "sk-own"}, http.StatusOK)
	if m := ada.expect(http.MethodGet, list, nil, http.StatusOK); m["listed"] != false || len(m["voices"].([]any)) != 0 {
		t.Fatalf("a provider without a list: %v", m)
	}
	fallback, _ := admin.expect(http.MethodGet, "/api/v1/agents/"+firstID+"/spoken-voice", nil, http.StatusOK)["spoken"].(map[string]any)
	if fallback["source"] != "provider-default" || fallback["name"] != "af_bella" {
		t.Fatalf("without a list: %v", fallback)
	}

	// Back to educa AI: settings changed, the list is read again.
	admin.expect(http.MethodPatch, "/api/v1/org/voice-provider", map[string]any{"base_url": ""}, http.StatusOK)
	if m := ada.expect(http.MethodGet, list, nil, http.StatusOK); m["listed"] != true {
		t.Fatalf("educa again: %v", m)
	}
	educa.mu.Lock()
	reads = educa.listReads
	educa.mu.Unlock()
	if reads != 2 {
		t.Fatalf("the list was read %d times, want twice", reads)
	}
}
