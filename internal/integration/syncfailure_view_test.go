package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

/* Die Arbeitsplatz-Ansicht zeigte den letzten geglückten Schnappschuss — wahr
   und nutzlos, während seither jeder Versuch scheiterte. Auf einer produktiven
   Instanz war das wochenlang so und kostete einen 39-Minuten-Lauf (#72).

   Geprüft wird hier die Auskunft, nicht der Fehlschlag selbst: dass ein
   fehlgeschlagener Versuch überhaupt aufgeschrieben wird, steht im Runner-Test
   daneben. Hier zählt, was ein Mensch danach sieht. */

func TestDieArbeitsplatzAnsichtSagtWennSeitherNichtsGesichertWurde(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("das Fake-Binary ist ein Shell-Skript")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, _, _ := filesStack(t, dir)
	c := login(t, s, "admin@test.local", "admin-passwort")

	agent := s.newSupportAgent("sync-fehlschlag")
	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Write("arbeit/ergebnis.md", strings.NewReader("etwas Arbeit")); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)

	lies := func() struct {
		Latest *struct {
			CreatedAt time.Time `json:"created_at"`
		} `json:"latest"`
		LastFailure *struct {
			At     time.Time `json:"at"`
			Error  string    `json:"error"`
			Reason string    `json:"reason"`
		} `json:"last_failure"`
	} {
		t.Helper()
		var view struct {
			Latest *struct {
				CreatedAt time.Time `json:"created_at"`
			} `json:"latest"`
			LastFailure *struct {
				At     time.Time `json:"at"`
				Error  string    `json:"error"`
				Reason string    `json:"reason"`
			} `json:"last_failure"`
		}
		resp := c.do(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/home", nil)
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
			t.Fatal(err)
		}
		return view
	}

	view := lies()
	if view.Latest == nil {
		t.Fatal("ohne Schnappschuss prüft dieser Test nichts")
	}
	if view.LastFailure != nil {
		t.Fatalf("es ist nichts schiefgegangen und es steht trotzdem etwas da: %+v", view.LastFailure)
	}

	// Ein Fehlschlag NACH dem Schnappschuss — so, wie ihn die Steuerebene
	// aufschreibt (cmd/covey: SnapshotFailed).
	schreibeFehlschlag(t, s, agent.ID, agent.OrgID, "block 78a279df: 413 Request Entity Too Large",
		// Kurz NACH dem Schnappschuss, nicht in der Zukunft: der zweite
		// Schnappschuss unten entsteht jetzt und muss ihn überholen können.
		view.Latest.CreatedAt.Add(10*time.Millisecond))

	view = lies()
	if view.LastFailure == nil {
		t.Fatal("der Fehlschlag steht nicht in der Ansicht — genau die Stille aus #72")
	}
	if view.LastFailure.Error == "" || view.LastFailure.Reason != "job" {
		t.Fatalf("die Auskunft ist unvollständig: %+v", view.LastFailure)
	}

	// Und er verschwindet wieder, sobald ein neuer Schnappschuss ihn überholt:
	// „es hat seither geklappt" ist eine Auskunft, die niemand suchen muss.
	if _, err := tree.Write("arbeit/mehr.md", strings.NewReader("noch etwas")); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)
	if view := lies(); view.LastFailure != nil {
		t.Fatalf("der überholte Fehlschlag steht weiter da: %+v", view.LastFailure)
	}
}

func schreibeFehlschlag(t *testing.T, s *stack, agentID, orgID uuid.UUID, msg string, wann time.Time) {
	t.Helper()
	payload := map[string]any{
		"status": "preparing", "phase": "home_sync", "done": true,
		"error": msg, "detail": "job",
	}
	roh, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO recording_events (org_id, agent_id, kind, payload, created_at)
		 VALUES ($1,$2,'lifecycle',$3,$4)`, orgID, agentID, roh, wann); err != nil {
		t.Fatal(err)
	}
}
