package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

/* Die Wahl eines Arbeitsplatzes war eine ohne Preisschild: Auf einer gemessenen
   Instanz trugen fünf von acht Agenten eine Compiler-Kette, um Wiki-Seiten zu
   schreiben (#104). Seit die Phasen sichtbar sind, misst die Plattform, was ein
   Abruf kostet — diese Zahl gehört dorthin, wo jemand wählt. */

func TestDerArbeitsplatzNenntWasSeinAbrufGekostetHat(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("hat-gewaehlt")

	lies := func() map[string]struct {
		Bytes int64  `json:"bytes"`
		MS    int64  `json:"ms"`
		At    string `json:"at"`
	} {
		t.Helper()
		var liste []struct {
			Name     string `json:"name"`
			Image    string `json:"image"`
			LastPull *struct {
				Bytes int64  `json:"bytes"`
				MS    int64  `json:"ms"`
				At    string `json:"at"`
			} `json:"last_pull"`
		}
		resp := c.do(http.MethodGet, "/api/v1/workplaces", nil)
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(&liste); err != nil {
			t.Fatal(err)
		}
		out := map[string]struct {
			Bytes int64  `json:"bytes"`
			MS    int64  `json:"ms"`
			At    string `json:"at"`
		}{}
		for _, w := range liste {
			if w.LastPull != nil {
				out[w.Name] = struct {
					Bytes int64  `json:"bytes"`
					MS    int64  `json:"ms"`
					At    string `json:"at"`
				}{w.LastPull.Bytes, w.LastPull.MS, w.LastPull.At}
			}
		}
		return out
	}

	// Ohne gemessenen Abruf steht dort nichts — das ist etwas anderes als
	// „kostet nichts" und darf nicht so aussehen.
	if got := lies(); len(got) != 0 {
		t.Fatalf("ohne Messung wurde etwas behauptet: %+v", got)
	}

	// Ein abgeschlossener Bild-Abruf, wie ihn der Runner meldet und die
	// Steuerebene aufschreibt (cmd/covey: runnerPool.Progress).
	// Das Bild des base-Profils — dasselbe, das die Liste oben ausweist.
	const image = "covey-sandbox:latest"
	payload, _ := json.Marshal(map[string]any{
		"status": "preparing", "phase": "image", "done": true,
		"detail": image, "bytes": 2_411_724_800, "ms": 254_000,
	})
	if _, err := s.pool.Exec(ctx, `INSERT INTO recording_events (org_id, agent_id, kind, payload, created_at)
		VALUES ($1,$2,'lifecycle',$3,$4)`, s.orgID, agent.ID, payload, time.Now()); err != nil {
		t.Fatal(err)
	}

	got := lies()
	base, ok := got["base"]
	if !ok {
		t.Fatalf("der gemessene Abruf steht bei keinem Arbeitsplatz: %+v", got)
	}
	if base.Bytes != 2_411_724_800 || base.MS != 254_000 {
		t.Fatalf("die Zahlen kamen nicht durch: %+v", base)
	}
}
