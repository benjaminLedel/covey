package integration

import (
	"context"
	"net/http"
	"testing"

	runnerstore "covey/internal/runner/store"
)

// Ein Update wird abgelehnt, solange der Host Sandboxen trägt — und bis hierher
// blieb das Warten am Menschen hängen: drücken, abgelehnt, später nochmal. Auf
// einer Produktivinstanz hat das zwei Stunden gekostet, während ausgerechnet
// der Agent, dessen Sandbox blockierte, an dem Fehler litt, den das Update
// behebt.
//
// Der Wunsch gehört deshalb an die Zeile des Runners, nicht in den Kopf des
// Operators — und er muss einen Neustart der Steuerebene überleben.
func TestEinGeplantesUpdateStehtAnDerRunnerZeile(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	tokens := runnerstore.NewBuiltinTokens(s.runners)
	runnerID, _, err := tokens.For(ctx, s.orgID)
	if err != nil {
		t.Fatalf("Runner: %v", err)
	}

	// Vormerken, wie es die Steuerebene tut, wenn ein Host beschäftigt ist.
	if err := s.runners.PlanUpdate(ctx, runnerID, "v9.9.9"); err != nil {
		t.Fatalf("PlanUpdate: %v", err)
	}
	geplant, err := s.runners.PlannedUpdate(ctx, runnerID)
	if err != nil || geplant != "v9.9.9" {
		t.Fatalf("der Plan wurde nicht gespeichert: %q (%v)", geplant, err)
	}

	// Und er ist dort sichtbar, wo der Runner verwaltet wird.
	liste := c.expectList(http.MethodGet, "/api/v1/runners", nil, http.StatusOK)
	var gefunden bool
	for _, r := range liste {
		if r["id"] == runnerID.String() {
			gefunden = true
			if r["update_to"] != "v9.9.9" {
				t.Errorf("die Runner-Ansicht zeigt den Plan nicht: %v", r["update_to"])
			}
			if r["update_planned_at"] == nil {
				t.Error("seit wann geplant wird, gehört dazu")
			}
		}
	}
	if !gefunden {
		t.Fatal("der Runner steht nicht in der Liste")
	}

	// Zurücknehmen können muss man es auch — über eine eigene Route, weil eine
	// leere Version beim Update „die neueste" heißt und ein Feld nicht beides
	// bedeuten kann.
	c.expect(http.MethodDelete, "/api/v1/runners/"+runnerID.String()+"/update", nil, http.StatusOK)
	geplant, err = s.runners.PlannedUpdate(ctx, runnerID)
	if err != nil || geplant != "" {
		t.Fatalf("der Plan wurde nicht zurückgenommen: %q (%v)", geplant, err)
	}
}
