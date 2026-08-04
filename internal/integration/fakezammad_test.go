package integration

import (
	"encoding/json"
	"net/http"
	"strings"
)

// httpHandler implements the Zammad endpoints the agent uses.
func httpHandler(f *fakeZammad) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "number": "20042", "title": "Login funktioniert nicht",
			"state": "open", "customer_id": 7,
		})
	})

	mux.HandleFunc("GET /api/v1/ticket_articles/by_ticket/{id}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "ticket_id": 42, "sender": "Customer", "body": "Ich kann mich nicht einloggen."},
		})
	})

	mux.HandleFunc("POST /api/v1/ticket_articles", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.record("reply", body)
		json.NewEncoder(w).Encode(map[string]any{"id": 99})
	})

	mux.HandleFunc("PUT /api/v1/tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.record("update", body)
		json.NewEncoder(w).Encode(map[string]any{"id": 42})
	})

	return mux
}

func authorized(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Token token=")
}
