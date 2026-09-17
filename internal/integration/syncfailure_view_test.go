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

/* The workplace view showed the last successful snapshot — true and useless
   while every attempt since then failed. On a productive instance that stood
   for weeks and cost a 39-minute run (#72).

   Checked here is the report, not the failure itself: that a failed attempt
   gets written down at all stands in the Runner test next door. What counts
   here is what a person sees afterwards. */

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

	// A failure AFTER the snapshot — the way the control plane writes it
	// down (cmd/covey: SnapshotFailed).
	schreibeFehlschlag(t, s, agent.ID, agent.OrgID, "block 78a279df: 413 Request Entity Too Large",
		// Just AFTER the snapshot, not in the future: the second snapshot
		// below is created shortly and must be able to overtake it. One
		// millisecond, and then the loop waits — the whole test runs through
		// in ten milliseconds, and with ten milliseconds of lead the second
		// snapshot came to be BEFORE the failure it should overtake.
		view.Latest.CreatedAt.Add(time.Millisecond))

	view = lies()
	if view.LastFailure == nil {
		t.Fatal("der Fehlschlag steht nicht in der Ansicht — genau die Stille aus #72")
	}
	if view.LastFailure.Error == "" || view.LastFailure.Reason != "job" {
		t.Fatalf("die Auskunft ist unvollständig: %+v", view.LastFailure)
	}

	// And it disappears again as soon as a new snapshot overtakes it: "it
	// worked since then" is a report nobody has to go looking for.
	//
	// Wait until the clock stands above the failure: otherwise the test
	// checks the speed of the machine, not the overtaking.
	for time.Now().Before(view.LastFailure.At.Add(2 * time.Millisecond)) {
		time.Sleep(time.Millisecond)
	}
	if _, err := tree.Write("arbeit/mehr.md", strings.NewReader("noch etwas")); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)
	if v := lies(); v.LastFailure != nil {
		t.Fatalf("der überholte Fehlschlag steht weiter da: %+v (Schnappschuss jetzt %v, vorher %v)",
			v.LastFailure, v.Latest.CreatedAt, view.Latest.CreatedAt)
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
