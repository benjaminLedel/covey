// fakezammad ist ein minimales Zammad-Double für lokale Demos: die vier vom
// Support-Agenten genutzten Endpunkte, alle Zugriffe werden geloggt.
// Start: go run ./demo/fakezammad (Port 9999).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("→ GET ticket %s (auth: %s)", r.PathValue("id"), authShape(r))
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
	srv := &http.Server{Addr: ":9999", Handler: mux, ReadHeaderTimeout: 20 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// authShape zeigt, DASS ein gebrokertes Credential ankommt — Schema und Länge
// statt des Tokens selbst. Für die Demo ist genau das die Aussage; das Token
// gehört auch in einem Double nicht ins Log.
func authShape(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "keine"
	}
	scheme, rest, ok := strings.Cut(h, " ")
	if !ok {
		return fmt.Sprintf("%d Zeichen", len(h))
	}
	return fmt.Sprintf("%s, %d Zeichen", scheme, len(rest))
}
