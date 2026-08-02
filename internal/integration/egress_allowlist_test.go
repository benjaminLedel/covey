package integration

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// EffectiveAllowlist entscheidet, welche Hosts eine Sandbox überhaupt erreicht
// — der Egress-Wächter aus spec/06. Die Funktion war ungeprüft, obwohl sie drei
// Quellen zusammenführt (zugewiesene Vorlagen, agent-eigene Hosts, Org-Defaults)
// und ihr Ergebnis darüber entscheidet, wohin ein Agent Daten schicken kann.
//
// Zwei Dinge müssen stimmen: Es muss ALLES drin sein, was erlaubt ist (fehlt
// etwas, bricht die Arbeit des Agenten grundlos ab) — und NICHTS, was einer
// anderen Organisation gehört.
func TestEffectiveAllowlist(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("egress-agent")

	// Quelle 1: eine Vorlage, dem Agenten zugewiesen.
	vorlage, err := s.egress.CreateTemplate(ctx, s.orgID, "ticketsystem", "Zammad & Co.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.egress.AddTemplateHost(ctx, s.orgID, vorlage.ID, "zammad.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.egress.AddTemplateHost(ctx, s.orgID, vorlage.ID, "*.zammad-cdn.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.egress.SetAgentTemplate(ctx, agent.ID, vorlage.ID, true); err != nil {
		t.Fatal(err)
	}

	// Quelle 2: ein Host, den nur dieser Agent hat.
	if _, err := s.egress.AddAgentHost(ctx, agent.ID, "eigener-host.example.com", "Sonderfall"); err != nil {
		t.Fatal(err)
	}

	// Quelle 3: ein Default der Organisation — gilt für alle ihre Agenten.
	if _, err := s.egress.AddDefaultHost(ctx, s.orgID, "api.anthropic.com", "Runtime"); err != nil {
		t.Fatal(err)
	}

	// Eine fremde Organisation mit eigenen Defaults und eigener Vorlage. Nichts
	// davon darf in unserer Allowlist auftauchen.
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-Egress')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.egress.AddDefaultHost(ctx, fremdeOrg, "fremder-default.example.com", ""); err != nil {
		t.Fatal(err)
	}
	fremdeVorlage, err := s.egress.CreateTemplate(ctx, fremdeOrg, "fremd", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.egress.AddTemplateHost(ctx, fremdeOrg, fremdeVorlage.ID, "fremder-host.example.com", ""); err != nil {
		t.Fatal(err)
	}

	liste, err := s.egress.EffectiveAllowlist(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(liste)
	drin := func(p string) bool {
		for _, l := range liste {
			if l == p {
				return true
			}
		}
		return false
	}

	// Alle drei Quellen fließen zusammen.
	for _, erwartet := range []string{
		"zammad.example.com", "*.zammad-cdn.example.com", // Vorlage
		"eigener-host.example.com", // agent-eigen
		"api.anthropic.com",        // Org-Default
	} {
		if !drin(erwartet) {
			t.Errorf("%q fehlt in der Allowlist: %v", erwartet, liste)
		}
	}
	// Und nichts Fremdes.
	for _, verboten := range []string{"fremder-default.example.com", "fremder-host.example.com"} {
		if drin(verboten) {
			t.Errorf("%q gehört einer anderen Organisation, steht aber in der Allowlist: %v", verboten, liste)
		}
	}

	// Ein Agent OHNE Zuweisungen bekommt nur die Org-Defaults — nicht die
	// Hosts seines Kollegen. Sonst wäre jede agent-eigene Freigabe eine
	// org-weite.
	kollege := s.newSupportAgent("egress-kollege")
	kollegenListe, err := s.egress.EffectiveAllowlist(ctx, kollege.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kollegenListe) != 1 || kollegenListe[0] != "api.anthropic.com" {
		t.Errorf("Kollege ohne eigene Freigaben sieht %v, erwartet nur den Org-Default", kollegenListe)
	}

	// Die Vorlage abziehen nimmt ihre Hosts wieder mit.
	if err := s.egress.SetAgentTemplate(ctx, agent.ID, vorlage.ID, false); err != nil {
		t.Fatal(err)
	}
	danach, err := s.egress.EffectiveAllowlist(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(danach, ","), "zammad") {
		t.Errorf("nach dem Abziehen der Vorlage stehen ihre Hosts noch drin: %v", danach)
	}
	if !strings.Contains(strings.Join(danach, ","), "eigener-host") {
		t.Errorf("das Abziehen der Vorlage hat den agent-eigenen Host mitgenommen: %v", danach)
	}
}
