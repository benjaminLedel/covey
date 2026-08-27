package homestore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Die Bündelfrage ist der Unterschied zwischen einem Home, das synchronisiert
// wird, und einem, das es nicht mehr schafft: sechsstellig viele Runden über
// die Leitung werden zu dreistellig vielen.
func TestDieBuendelfrageGehtAlsEineAnfrageRaus(t *testing.T) {
	var anfragen int
	var bekommen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/runner/v1/blocks-have" || r.Method != http.MethodPost {
			t.Errorf("unerwarteter Aufruf: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		anfragen++
		var in struct {
			Hashes []string `json:"hashes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		bekommen = in.Hashes
		// Nur der zweite ist bekannt.
		_ = json.NewEncoder(w).Encode(map[string]any{"have": []string{in.Hashes[1]}})
	}))
	defer srv.Close()

	h := NewHTTPStore(srv.URL, "tok")
	have, err := h.HasMany(context.Background(), uuid.New(), []string{"aaa", "bbb", "ccc"})
	if err != nil {
		t.Fatal(err)
	}
	if anfragen != 1 {
		t.Errorf("%d Anfragen für drei Blöcke", anfragen)
	}
	if len(bekommen) != 3 {
		t.Errorf("die Frage trug %d Hashes statt drei", len(bekommen))
	}
	if !have["bbb"] || have["aaa"] || have["ccc"] {
		t.Errorf("die Antwort wurde falsch gelesen: %v", have)
	}
}

// Eine Steuerebene von vor der Bündelfrage kennt die Route nicht. Dann wird
// einzeln gefragt — ein langsamer Sync ist besser als ein falscher.
func TestGegenEineAeltereSteuerebeneWirdEinzelnGefragt(t *testing.T) {
	var kopfanfragen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/runner/v1/blocks-have":
			w.WriteHeader(http.StatusNotFound) // Route gibt es hier nicht
		case r.Method == http.MethodHead:
			kopfanfragen++
			if r.URL.Path == "/api/runner/v1/blocks/bbb" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h := NewHTTPStore(srv.URL, "tok")
	have, err := h.HasMany(context.Background(), uuid.New(), []string{"aaa", "bbb"})
	if err != nil {
		t.Fatal(err)
	}
	if kopfanfragen != 2 {
		t.Errorf("erwartet zwei Einzelfragen, gezählt %d", kopfanfragen)
	}
	if !have["bbb"] || have["aaa"] {
		t.Errorf("die Einzelantworten wurden falsch zusammengesetzt: %v", have)
	}
}
