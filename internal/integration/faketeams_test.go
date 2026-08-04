package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeTeams is the target-system double: the OAuth2 token endpoint, the bot
// connector (send/reply) and a pre-authorized file endpoint for attachments.
// It records every access.
type fakeTeams struct {
	mu        sync.Mutex
	tokenHits int
	replies   []map[string]string // {cid, aid, text, auth}
	sends     []map[string]string // {cid, text}
	files     []string
	cards     []map[string]any // posted cards (file.consent / file.info)
	uploads   []map[string]string
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

	// Reply to a concrete message.
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

	// A new message into a conversation — with attachments it is a card
	// (consent resp. completion of the file transfer), otherwise plain text.
	mux.HandleFunc("POST /v3/conversations/{cid}/activities", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		if ats, ok := body["attachments"].([]any); ok && len(ats) > 0 {
			at, _ := ats[0].(map[string]any)
			f.cards = append(f.cards, at)
		} else {
			f.sends = append(f.sends, map[string]string{"cid": r.PathValue("cid"), "text": fmt.Sprint(body["text"])})
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-2"})
	})

	// Upload session as Teams hands it over after the consent: PUT with
	// Content-Range and without a connector token.
	mux.HandleFunc("PUT /upload/{name}", func(w http.ResponseWriter, r *http.Request) {
		buf := new(strings.Builder)
		io.Copy(buf, r.Body)
		f.mu.Lock()
		f.uploads = append(f.uploads, map[string]string{
			"name":  r.PathValue("name"),
			"body":  buf.String(),
			"range": r.Header.Get("Content-Range"),
			"auth":  r.Header.Get("Authorization"),
		})
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": "uploaded"})
	})

	// Pre-authorized file download (without a token) — target of download_attachment.
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

// cardsOfType returns the posted cards of one content type
// (file.consent / file.info).
func (f *fakeTeams) cardsOfType(suffix string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.cards {
		if strings.HasSuffix(fmt.Sprint(c["contentType"]), suffix) {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeTeams) lastUpload() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.uploads) == 0 {
		return nil
	}
	return f.uploads[len(f.uploads)-1]
}

func (f *fakeTeams) fileHits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.files)
}
