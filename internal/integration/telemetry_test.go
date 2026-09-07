package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"covey/internal/settings"
	"covey/internal/telemetry"
)

// TestTelemetrieSchicktNurZahlen ist die Zusage, die in der Dokumentation
// steht, als Test: Was diese Installation einmal am Tag hinausschickt, sind
// Zahlen und Versionen — kein Aufgabentitel, kein Agenten-Slug, kein Text,
// keine Adresse.
//
// Der Test ist absichtlich stur formuliert: Er nimmt den Titel einer echten
// Aufgabe und den Slug eines echten Agenten aus dieser Instanz und sucht sie
// im gesendeten Koerper. Wer spaeter ein Feld hinzufuegt, das mehr traegt als
// eine Zahl, faellt hier auf und nicht bei einem Kunden.
func TestTelemetrieSchicktNurZahlen(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent := s.newSupportAgent("geheimer-slug")
	if _, err := s.backlog.Create(ctx, s.orgID, agent.ID,
		"Ein Titel, der niemanden etwas angeht", "Und ein Text dazu.", "manual", 3); err != nil {
		t.Fatal(err)
	}

	empfangen := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		roh, _ := io.ReadAll(r.Body)
		if !strings.HasSuffix(r.URL.Path, "/telemetrie") {
			t.Errorf("die Zahlen gehoeren an /telemetrie, nicht an %q", r.URL.Path)
		}
		empfangen <- roh
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	if err := s.settings.Set(ctx, settings.TelemetryURL, server.URL, nil); err != nil {
		t.Fatal(err)
	}
	sender := &telemetry.Sender{Pool: s.pool, Settings: s.settings, Log: slog.Default()}
	sender.Send(ctx)

	var roh []byte
	select {
	case roh = <-empfangen:
	default:
		t.Fatal("es wurde nichts geschickt — Telemetrie ist standardmaessig an")
	}

	var koerper struct {
		Kennung string         `json:"kennung"`
		Version string         `json:"version"`
		Zahlen  map[string]any `json:"zahlen"`
	}
	if err := json.Unmarshal(roh, &koerper); err != nil {
		t.Fatalf("der Koerper ist kein JSON: %v", err)
	}
	if koerper.Kennung == "" {
		t.Fatal("ohne Kennung sind die Zahlen von gestern und heute nicht dieselbe Installation")
	}
	for _, feld := range []string{"orgs", "agents", "tasks_24h", "runtimes", "targets"} {
		if _, ok := koerper.Zahlen[feld]; !ok {
			t.Errorf("das Feld %q fehlt in den Zahlen: %v", feld, koerper.Zahlen)
		}
	}

	// Und jetzt die eigentliche Zusage.
	for _, verboten := range []string{
		"geheimer-slug",
		"Ein Titel, der niemanden etwas angeht",
		"Und ein Text dazu.",
		"admin@test.local",
	} {
		if strings.Contains(string(roh), verboten) {
			t.Fatalf("das darf die Maschine nicht verlassen: %q\n%s", verboten, roh)
		}
	}

	// Aus heisst aus: kein zweiter Versuch, keine halbe Menge.
	if err := s.settings.Set(ctx, settings.TelemetryMode, settings.Off, nil); err != nil {
		t.Fatal(err)
	}
	sender.Send(ctx)
	select {
	case roh := <-empfangen:
		t.Fatalf("abgeschaltet darf nichts mehr hinausgehen: %s", roh)
	default:
	}

	// Und die zweite Art, sie abzuschalten: die Umgebung, fuer den, der die
	// Oberflaeche gar nicht erst starten will.
	if err := s.settings.Set(ctx, settings.TelemetryMode, settings.On, nil); err != nil {
		t.Fatal(err)
	}
	(&telemetry.Sender{Pool: s.pool, Settings: s.settings, Log: slog.Default(), Env: "off"}).Send(ctx)
	select {
	case roh := <-empfangen:
		t.Fatalf("COVEY_TELEMETRY=off muss ueber der Einstellung stehen: %s", roh)
	default:
	}
}
