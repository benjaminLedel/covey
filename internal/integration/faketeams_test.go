package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeTeams ist das Zielsystem-Double: der OAuth2-Token-Endpoint, der Bot
// Connector (senden/antworten) und ein vor-autorisierter Datei-Endpunkt für
// Anhänge. Es zeichnet alle Zugriffe auf.
type fakeTeams struct {
	mu        sync.Mutex
	tokenHits int
	replies   []map[string]string // {cid, aid, text, auth}
	sends     []map[string]string // {cid, text}
	files     []string
	srv       *httptest.Server
}

func newFakeTeams(t *testing.T) *fakeTeams {
	f := &fakeTeams{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.tokenHits++
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"access_token": "connector-token", "expires_in": 3600})
	})

	// Antwort auf eine konkrete Nachricht.
	mux.HandleFunc("POST /v3/conversations/{cid}/activities/{aid}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.replies = append(f.replies, map[string]string{
			"cid": r.PathValue("cid"), "aid": r.PathValue("aid"),
			"text": fmt.Sprint(body["text"]), "auth": r.Header.Get("Authorization"),
		})
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1"})
	})

	// Neue Nachricht in eine Konversation.
	mux.HandleFunc("POST /v3/conversations/{cid}/activities", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.sends = append(f.sends, map[string]string{"cid": r.PathValue("cid"), "text": fmt.Sprint(body["text"])})
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-2"})
	})

	// Vor-autorisierter Datei-Download (ohne Token) — Ziel von download_attachment.
	mux.HandleFunc("GET /files/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		f.mu.Lock()
		f.files = append(f.files, name)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("Anhang-Inhalt für " + name))
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTeams) replyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.replies)
}

func (f *fakeTeams) lastReply() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.replies) == 0 {
		return nil
	}
	return f.replies[len(f.replies)-1]
}

func (f *fakeTeams) fileHits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.files)
}
