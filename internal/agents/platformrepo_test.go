package agents

import (
	"strings"
	"testing"

	"covey/internal/buildinfo"
)

// Wohin covey Doctor meldet, wenn niemand es ihm gesagt hat: in das Projekt,
// aus dem dieses Programm stammt. Die Adresse stand vorher als Frage in den
// Stammdaten — dabei weiss die Plattform sie ueber sich selbst (spec/21).
func TestPlatformRepoVoreinstellung(t *testing.T) {
	standardSystem, standardProjekt := buildinfo.SourceRepo()
	if standardSystem == "" || standardProjekt == "" {
		t.Fatal("SourceURL laesst sich nicht als Zielsystem-Adresse lesen — dann gibt es keine Voreinstellung")
	}

	faelle := []struct {
		name            string
		system, projekt string
		wantS, wantP    string
	}{
		{"nichts gespeichert → das eigene Projekt", "", "", standardSystem, standardProjekt},
		{"halbe Adresse zaehlt nicht", "gitlab", "", standardSystem, standardProjekt},
		{"eigenes Repository gewinnt", "gitlab", "gruppe/covey", "gitlab", "gruppe/covey"},
		{"abgeschaltet heisst gar nicht", RepoOff, "", "", ""},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			s, p := PlatformRepo(f.system, f.projekt)
			if s != f.wantS || p != f.wantP {
				t.Fatalf("PlatformRepo(%q,%q) = %q,%q — erwartet %q,%q", f.system, f.projekt, s, p, f.wantS, f.wantP)
			}
		})
	}
}

// Der Abschnitt im Prompt haengt am Anker: Ein Agent, der den Default-Branch
// liest, meldet gegen Code, den diese Instanz nicht ausfuehrt.
func TestPlatformRepoDocAnker(t *testing.T) {
	mitTag := PlatformRepoDoc("github", "benjaminLedel/covey", "v0.4.0", true)
	if !strings.Contains(mitTag, "`ref: v0.4.0`") || !strings.Contains(mitTag, "release") {
		t.Fatalf("der Tag soll als Auslieferung benannt werden:\n%s", mitTag)
	}

	mitCommit := PlatformRepoDoc("github", "benjaminLedel/covey", "abc1234", false)
	if !strings.Contains(mitCommit, "`ref: abc1234`") || !strings.Contains(mitCommit, "commit") {
		t.Fatalf("ohne Tag bleibt der Commit der Anker:\n%s", mitCommit)
	}

	// Ohne Provenance keine Behauptung: dann steht im Prompt, dass die
	// Zuordnung fehlt — und nicht der Default-Branch als „laufender Stand".
	ohne := PlatformRepoDoc("github", "benjaminLedel/covey", "", false)
	if !strings.Contains(ohne, "default branch") || !strings.Contains(ohne, "no version information") {
		t.Fatalf("ohne Anker fehlt die ehrliche Auskunft:\n%s", ohne)
	}

	// Keine Adresse, kein Abschnitt — sonst stuende im Prompt eine Faehigkeit,
	// die der Broker gleich darauf abweist.
	if PlatformRepoDoc("", "", "v0.4.0", true) != "" {
		t.Fatal("ohne Adresse darf kein Abschnitt entstehen")
	}
}
