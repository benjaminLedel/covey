package integration

import (
	"strings"
	"testing"

	"covey/internal/config"
	"covey/internal/doctor"
	"covey/internal/workplaces"

	"github.com/google/uuid"
)

/*
Der eine Zusammenstoss, den ein Upgrade still erzeugen kann.

	Ein veroeffentlichter Profilname schlaegt beim Aufloesen den gleichnamigen
	eigenen Arbeitsplatz (runner.Pool.imageFor), und das ist Absicht: Welches
	Image ein Name meint, darf nicht davon abhaengen, wer zuerst nachsieht.
	Anlegen laesst sich ein solcher Name deshalb gar nicht erst.

	Was sich nicht verbieten laesst, ist die andere Richtung: ein Name, den
	jemand letzten Monat vergeben hat und den das Projekt diesen Monat
	veroeffentlicht. Seit es die Rollen-Arbeitsplaetze gibt (#112), ist das kein
	gedachter Fall mehr. Der Agent startet dann ein anderes Image als das
	eingetragene — und niemand sagt es ihm. `covey doctor` sagt es.
*/
func TestEinEigenerNameDenDasProjektSpaeterVeroeffentlicht(t *testing.T) {
	s := newStack(t)
	store := workplaces.New(s.pool)

	// Ueber den Store und nicht ueber die API: Die API weist den Namen ab, und
	// genau so entsteht der Fall auch in Wirklichkeit — er war frei, als er
	// vergeben wurde.
	if _, err := store.Create(t.Context(), s.orgID, uuid.Nil, workplaces.Workplace{
		Name: "dev-flutter", Label: "Unser Flutter",
		Image: "registry.example.com/team/eigenes:1",
	}); err != nil {
		t.Fatalf("Arbeitsplatz anlegen: %v", err)
	}

	report := doctor.RunWith(t.Context(), config.Config{}, s.pool)
	var gefunden bool
	for _, f := range report.Findings {
		if f.What != "workplace dev-flutter" {
			continue
		}
		gefunden = true
		if f.OK {
			t.Errorf("der Zusammenstoss wird als in Ordnung gemeldet: %+v", f)
		}
		// Beide Images muessen dastehen, sonst ist die Meldung eine Behauptung
		// ohne Nachweis: das gestartete und das gemeinte.
		if !strings.Contains(f.Detail, "registry.example.com/team/eigenes:1") {
			t.Errorf("das eigene Image fehlt in der Meldung: %q", f.Detail)
		}
		if !strings.Contains(f.Detail, "covey-sandbox-dev-flutter") {
			t.Errorf("das veroeffentlichte Image fehlt in der Meldung: %q", f.Detail)
		}
		if f.Remedy == "" {
			t.Error("eine Meldung ohne Ausweg ist Moebel")
		}
	}
	if !gefunden {
		t.Fatalf("der Zusammenstoss wurde nicht gemeldet: %+v", report.Findings)
	}
}
