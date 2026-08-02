// faketeams ist ein minimales Bot-Connector-Double für lokale Demos: der
// OAuth2-Token-Endpoint und die drei vom Teams-Agenten genutzten
// Connector-Endpunkte (senden, antworten, Konversation eröffnen). Alle
// Zugriffe werden geloggt.
//
// Start: go run ./demo/faketeams (Port 9998).
//
// Gegenrichtung (eingehende Nachricht → Covey wecken): Da faketeams die
// Antwort-Seite (Bot Connector) spielt, wird eine eingehende Activity per curl
// direkt an den Messaging-Endpoint geschickt (COVEY_TEAMS_WEBHOOK_SECRET leer
// lassen, dann ist die JWT-Prüfung aus):
//
//	curl -X POST http://localhost:8494/api/webhooks/teams/<agent-slug> \
//	  -H 'Content-Type: application/json' -d '{
//	    "type":"message","id":"a1","text":"Hallo Agent, siehe Anhang",
//	    "serviceUrl":"http://localhost:9998","channelId":"msteams",
//	    "from":{"id":"29:kunde","name":"Kunde"},
//	    "recipient":{"id":"28:bot","name":"Covey"},
//	    "conversation":{"id":"19:conv1","conversationType":"personal","tenantId":"t1"},
//	    "attachments":[{"contentType":"application/vnd.microsoft.teams.file.download.info",
//	      "name":"notiz.txt","content":{"downloadUrl":"http://localhost:9998/files/notiz.txt"}}]}'
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("→ POST /token (OAuth2 client_credentials)")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-connector-token",
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	})

	mux.HandleFunc("POST /v3/conversations/{cid}/activities", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		log.Printf("→ SEND conversation=%s text=%q", r.PathValue("cid"), body["text"])
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-1"})
	})

	mux.HandleFunc("POST /v3/conversations/{cid}/activities/{aid}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		log.Printf("→ REPLY conversation=%s activity=%s text=%q", r.PathValue("cid"), r.PathValue("aid"), body["text"])
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-2"})
	})

	mux.HandleFunc("POST /v3/conversations", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		log.Printf("→ CREATE conversation: %v", body["members"])
		json.NewEncoder(w).Encode(map[string]any{"id": "19:new-conv"})
	})

	// Datei-Anhang (vor-autorisiert, ohne Token) — Ziel eines download_attachment.
	mux.HandleFunc("GET /files/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		log.Printf("→ DOWNLOAD attachment %s", name)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("Beispiel-Anhang " + name + " aus fake-teams.\n"))
	})

	log.Println("fake-teams (Bot Connector) auf :9998")
	srv := &http.Server{Addr: ":9998", Handler: mux, ReadHeaderTimeout: 20 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
