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
The one collision that an upgrade can produce silently.

	A published profile name beats the own workplace of the same name when
	it resolves (runner.Pool.imageFor), and that is intended: which image a
	name means must not depend on who looks first. Such a name therefore
	cannot be created in the first place.

	What cannot be forbidden is the other direction: a name that someone
	gave last month and that the project publishes this month. Since the
	role workplaces exist (#112), that is no longer a hypothetical case.
	The agent then starts a different image than the registered one — and
	nobody tells it. `covey doctor` does.
*/
func TestEinEigenerNameDenDasProjektSpaeterVeroeffentlicht(t *testing.T) {
	s := newStack(t)
	store := workplaces.New(s.pool)

	// Through the store and not through the API: the API rejects the name, and
	// that is exactly how the case arises in reality too — it was free when it
	// was given out.
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
		// Both images have to stand there, otherwise the message is an
		// assertion without proof: the started one and the intended one.
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
