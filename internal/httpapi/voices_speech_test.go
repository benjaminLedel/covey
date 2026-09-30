package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"covey/internal/chat"
)

// TestConversationSpeechWithoutVoices: an instance without the voice store
// still answers the call's questions — the provider's default voice, a style
// line, and no address, so the app's greeting takes its own default (#506).
func TestConversationSpeechWithoutVoices(t *testing.T) {
	agent := uuid.New()
	c := chat.Conversation{ID: uuid.New(), Members: []chat.Member{{Kind: chat.MemberAgent, ID: agent}}}
	s := &Server{}

	w := httptest.NewRecorder()
	s.handleConversationSpeech(w, httptest.NewRequest(http.MethodGet, "/api/v1/conversations/x/speech?agent="+agent.String(), nil), c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if a, ok := out["address"]; !ok || a != "" {
		t.Fatalf("address: %v (present %v)", a, ok)
	}
	if out["instructions"] == "" || out["speech"] != nil {
		t.Fatalf("answer: %v", out)
	}

	w = httptest.NewRecorder()
	s.handleConversationSpeech(w, httptest.NewRequest(http.MethodGet, "/api/v1/conversations/x/speech?agent="+uuid.NewString(), nil), c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("an agent not in the conversation: %d", w.Code)
	}
}
