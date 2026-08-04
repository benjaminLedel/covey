package integration

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// EffectiveAllowlist decides which hosts a sandbox can reach at all — the
// egress guard from spec/06. The function was untested although it merges three
// sources (assigned templates, agent-owned hosts, org defaults) and its result
// decides where an agent can send data.
//
// Two things have to be right: EVERYTHING that is allowed has to be in there
// (if something is missing, the agent's work breaks off for no reason) — and
// NOTHING that belongs to another organization.
func TestEffectiveAllowlist(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("egress-agent")

	// Source 1: a template, assigned to the agent.
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

	// Source 2: a host that only this agent has.
	if _, err := s.egress.AddAgentHost(ctx, agent.ID, "eigener-host.example.com", "Sonderfall"); err != nil {
		t.Fatal(err)
	}

	// Source 3: an organization default — valid for all of its agents.
	if _, err := s.egress.AddDefaultHost(ctx, s.orgID, "api.anthropic.com", "Runtime"); err != nil {
		t.Fatal(err)
	}

	// A foreign organization with its own defaults and its own template. None of
	// that may show up in our allowlist.
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

	// All three sources flow together.
	for _, erwartet := range []string{
		"zammad.example.com", "*.zammad-cdn.example.com", // template
		"eigener-host.example.com", // agent-owned
		"api.anthropic.com",        // org default
	} {
		if !drin(erwartet) {
			t.Errorf("%q is missing from the allowlist: %v", erwartet, liste)
		}
	}
	// And nothing foreign.
	for _, verboten := range []string{"fremder-default.example.com", "fremder-host.example.com"} {
		if drin(verboten) {
			t.Errorf("%q belongs to another organization but is in the allowlist: %v", verboten, liste)
		}
	}

	// An agent WITHOUT assignments only gets the org defaults — not its
	// colleague's hosts. Otherwise every agent-owned clearance would be an
	// org-wide one.
	kollege := s.newSupportAgent("egress-kollege")
	kollegenListe, err := s.egress.EffectiveAllowlist(ctx, kollege.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kollegenListe) != 1 || kollegenListe[0] != "api.anthropic.com" {
		t.Errorf("the colleague without its own clearances sees %v, expected only the org default", kollegenListe)
	}

	// Withdrawing the template takes its hosts along again.
	if err := s.egress.SetAgentTemplate(ctx, agent.ID, vorlage.ID, false); err != nil {
		t.Fatal(err)
	}
	danach, err := s.egress.EffectiveAllowlist(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(danach, ","), "zammad") {
		t.Errorf("after withdrawing the template its hosts are still in there: %v", danach)
	}
	if !strings.Contains(strings.Join(danach, ","), "eigener-host") {
		t.Errorf("withdrawing the template took the agent-owned host with it: %v", danach)
	}
}
