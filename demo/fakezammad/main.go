// fakezammad ist ein minimales Zammad-Double für lokale Demos: die vier vom
// Support-Agenten genutzten Endpunkte, alle Zugriffe werden geloggt.
// Start: go run ./demo/fakezammad (Port 9999).
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("→ GET ticket %s (auth: %.30s)", r.PathValue("id"), r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "number": "20042", "title": "Login funktioniert nicht",
			"state": "open", "customer_id": 7,
		})
	})
	mux.HandleFunc("GET /api/v1/ticket_articles/by_ticket/{id}", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("→ GET articles für ticket %s", r.PathValue("id"))
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "ticket_id": 42, "sender": "Customer", "body": "Ich kann mich seit gestern nicht mehr einloggen."},
		})
	})
	mux.HandleFunc("POST /api/v1/ticket_articles", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		log.Printf("→ POST article: internal=%v body=%q", body["internal"], body["body"])
		json.NewEncoder(w).Encode(map[string]any{"id": 99})
	})
	mux.HandleFunc("PUT /api/v1/tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		log.Printf("→ PUT ticket %s: %v", r.PathValue("id"), body)
		json.NewEncoder(w).Encode(map[string]any{"id": 42})
	})

	log.Println("fake-zammad auf :9999")
	log.Fatal(http.ListenAndServe(":9999", mux))
}
