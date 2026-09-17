package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"covey/internal/agents"
)

// vorschlag files a proposal the way the action covey/propose_agent_config
// writes it later: the sender is an agent, the platform sets the base
// version.
func vorschlag(t *testing.T, s *stack, ziel, autor agents.Agent, files map[string]string) agents.ImprovementItem {
	t.Helper()
	item, err := s.registry.CreateImprovement(context.Background(), agents.ImprovementItem{
		OrgID: s.orgID, AgentID: ziel.ID, Kind: agents.KindProposal,
		Title:         "Teilergebnis vor dem Turn-Limit abschließen",
		Rationale:     "22 von 23 Läufen endeten am Turn-Limit, ohne Ergebnis.",
		Files:         files,
		AuthorAgentID: &autor.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// TestVorschlagLiegtUndLaeuftNicht is the property spec/21 rests on:
// covey Doctor proposes, it does not decide. An open
// proposal reaches the reviewed agent by no route — it is not a
// version, so there is no assembly it could end up in.
func TestVorschlagLiegtUndLaeuftNicht(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")

	item := vorschlag(t, s, ziel, autor, map[string]string{
		"PLAYBOOKS.md": "## Turn-Limit\n\nSchliesse das Teilergebnis ab, bevor du das Limit erreichst.",
	})
	if item.BaseVersion != 1 {
		t.Fatalf("die Basisversion schreibt die Plattform: %d", item.BaseVersion)
	}

	// The running config does not know it — neither as a file nor in the prompt.
	cfg := admin.expect(http.MethodGet, "/api/v1/agents/"+ziel.ID.String()+"/config", nil, http.StatusOK)
	if v := cfg["version"].(float64); v != 1 {
		t.Fatalf("ein Vorschlag darf keine Version erzeugen: %v", v)
	}
	files := cfg["files"].(map[string]any)
	if _, ok := files["PLAYBOOKS.md"]; ok {
		t.Fatal("die vorgeschlagene Datei darf nicht in der laufenden Config stehen")
	}
	if strings.Contains(cfg["compiled_prompt"].(string), "Turn-Limit") {
		t.Fatal("ein offener Vorschlag darf nicht im Systemprompt landen")
	}

	// The list shows it, with a diff and without a conflict.
	list := admin.expectList(http.MethodGet, "/api/v1/improvements?status=pending", nil, http.StatusOK)
	if len(list) != 1 {
		t.Fatalf("genau ein offener Punkt erwartet: %v", list)
	}
	got := list[0]
	if got["agent_slug"] != "kollege" || got["author_slug"] != "betrieb" {
		t.Fatalf("Betroffener und Absender müssen benannt sein: %v", got)
	}
	if got["needs_security"] != false || got["stale"] != false {
		t.Fatalf("ein Vorschlag zur PLAYBOOKS.md gegen die aktuelle Version: %v", got)
	}
	diff := got["diff"].([]any)
	if len(diff) != 1 || diff[0].(map[string]any)["file"] != "PLAYBOOKS.md" {
		t.Fatalf("der Diff muss genau die geänderte Datei tragen: %v", diff)
	}
	if diff[0].(map[string]any)["before"] != "" {
		t.Fatalf("vor der Annahme gibt es die Datei nicht: %v", diff[0])
	}

	// Accept: a new version over the normal write path, with the
	// human as author — and the previously untouched files are still there.
	decided := admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": true, "note": "Gute Beobachtung."}, http.StatusOK)
	if decided["status"] != "accepted" || decided["applied_version"].(float64) != 2 {
		t.Fatalf("die Annahme muss die erzeugte Version festhalten: %v", decided)
	}
	cfg = admin.expect(http.MethodGet, "/api/v1/agents/"+ziel.ID.String()+"/config", nil, http.StatusOK)
	files = cfg["files"].(map[string]any)
	if cfg["version"].(float64) != 2 || !strings.Contains(files["PLAYBOOKS.md"].(string), "Teilergebnis") {
		t.Fatalf("nach der Annahme läuft der Vorschlag: %v", cfg)
	}
	if !strings.Contains(files["SOUL.md"].(string), "Support") {
		t.Fatalf("gemergt, nicht ersetzt — die SOUL.md muss stehen bleiben: %v", files)
	}

	// A decision is taken once. The second click is not a second decision.
	admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusConflict)
}

// TestVorschlagAufAccessBrauchtSecurity: the depth decides who may accept.
// ACCESS.md and EGRESS.md are the text view of state whose write path
// sits with org_admin/security (spec/02) — a review dialog
// that lets everything through because the proposal looked harmless would move
// the access decision to whoever clicked first.
func TestVorschlagAufAccessBrauchtSecurity(t *testing.T) {
	s := newStack(t)
	s.mitglied(t, "owner@test.local", "Teamleiter", "agent_owner", "owner-passwort")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	owner := login(t, s, "owner@test.local", "owner-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")

	harmlos := vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})
	weitend := vorschlag(t, s, ziel, autor, map[string]string{
		"ACCESS.md": "- system: zammad scope: read,write,admin",
	})

	list := admin.expectList(http.MethodGet, "/api/v1/improvements?status=pending", nil, http.StatusOK)
	for _, it := range list {
		want := it["id"] == weitend.ID.String()
		if it["needs_security"] != want {
			t.Fatalf("needs_security falsch für %v: %v", it["title"], it)
		}
	}

	// The team lead may accept the playbook proposal.
	owner.expect(http.MethodPost, "/api/v1/improvements/"+harmlos.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)
	// Not the one that widens access.
	owner.expect(http.MethodPost, "/api/v1/improvements/"+weitend.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusForbidden)
	// Rejecting it he may do: that takes nothing away.
	abgelehnt := owner.expect(http.MethodPost, "/api/v1/improvements/"+weitend.ID.String()+"/decide",
		map[string]any{"accept": false, "note": "Nicht nötig."}, http.StatusOK)
	if abgelehnt["status"] != "rejected" || abgelehnt["decision_note"] != "Nicht nötig." {
		t.Fatalf("der Grund der Ablehnung muss stehen bleiben: %v", abgelehnt)
	}

	// And the rejected proposal stays readable — it is the most useful thing
	// for someone who audits covey Doctor themselves to read.
	rejected := admin.expectList(http.MethodGet, "/api/v1/improvements?status=rejected", nil, http.StatusOK)
	if len(rejected) != 1 {
		t.Fatalf("abgelehnte Punkte bleiben stehen: %v", rejected)
	}
}

// TestVorschlagUeberschreibtKeineFremdeÄnderung: a proposal is a diff
// against a base. If the same file is changed by hand in the meantime,
// it is not applied silently — the same conflict as on a pull
// request. A change to a DIFFERENT file only makes it stale.
func TestVorschlagUeberschreibtKeineFremdeÄnderung(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")

	item := vorschlag(t, s, ziel, autor, map[string]string{"SOUL.md": "# Support\n\nGeschaerft."})
	nebenbei := vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})

	// A human edits the SOUL.md.
	admin.expect(http.MethodPut, "/api/v1/agents/"+ziel.ID.String()+"/config",
		map[string]any{"files": map[string]string{
			"SOUL.md":   "# Support-Agent\n\nVon Hand geändert.",
			"ACCESS.md": "- system: zammad scope: read,write",
		}}, http.StatusOK)

	list := admin.expectList(http.MethodGet, "/api/v1/improvements?status=pending", nil, http.StatusOK)
	for _, it := range list {
		if it["stale"] != true {
			t.Fatalf("nach einer neuen Version sind beide veraltet: %v", it)
		}
		konflikt := it["id"] == item.ID.String()
		hat := it["conflicts"] != nil
		if hat != konflikt {
			t.Fatalf("nur die angefasste Datei ist ein Konflikt: %v", it)
		}
	}

	admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusConflict)
	// The stale but conflict-free proposal goes through.
	admin.expect(http.MethodPost, "/api/v1/improvements/"+nebenbei.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)
}

// TestSelbstvorschlagLiegtWieJederAndere: an agent may propose its OWN config
// — that is the open item from spec/20, and it is harmless,
// because nothing runs from here. Until a human accepts it, it changes nothing.
func TestSelbstvorschlagLiegtWieJederAndere(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	autor := s.newSupportAgent("personal")

	item, err := s.registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: s.orgID, AgentID: autor.ID, Kind: agents.KindProposal,
		Title: "Meine Rolle schaerfen", Rationale: "Nach dem ersten Auftrag weiss ich mehr.",
		Files: map[string]string{"SOUL.md": "# Personal\n\nGeschaerft."}, AuthorAgentID: &autor.ID,
	})
	if err != nil {
		t.Fatalf("der Vorschlag an sich selbst muss angelegt werden koennen: %v", err)
	}

	cfg, err := s.registry.CurrentConfig(ctx, autor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg.Files["SOUL.md"], "Geschaerft") {
		t.Fatal("ein Vorschlag darf sich nicht selbst in Kraft setzen")
	}

	admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)
	if cfg, err = s.registry.CurrentConfig(ctx, autor.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.Files["SOUL.md"], "Geschaerft") {
		t.Fatal("nach der Annahme durch einen Menschen laeuft er")
	}
}

// TestAnnahmeLaesstDenZugangInRuhe: accepting a proposal may change ONLY what
// the proposal contains.
//
// ACCESS.md and EGRESS.md do stand in every config version, but there they are
// not the live state: the tools tab writes the tool assignment
// without creating a version. If the snapshot went unfiltered into the
// write-through, accepting a proposal about PLAYBOOKS.md lifted the
// restriction someone had set over the UI again — from
// "get_ticket only" back to "all tools", without anyone seeing it.
func TestAnnahmeLaesstDenZugangInRuhe(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")

	// The restriction comes over the tools tab — without a config version.
	admin.expect(http.MethodPut, "/api/v1/agents/"+ziel.ID.String()+"/tools/zammad",
		map[string]any{"tools": []string{"get_ticket"}}, http.StatusOK)

	tools := func() int {
		t.Helper()
		var n int
		if err := s.pool.QueryRow(t.Context(),
			"SELECT COUNT(*) FROM agent_target_tools WHERE agent_id=$1 AND system='zammad'", ziel.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := tools(); n != 1 {
		t.Fatalf("Vorbedingung: genau ein erlaubtes Werkzeug erwartet, %d gefunden", n)
	}

	// A proposal that does not touch access at all.
	item := vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})
	admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)

	if n := tools(); n != 1 {
		t.Fatalf("die Annahme hat die Werkzeug-Einschraenkung angetastet: %d Eintraege statt 1", n)
	}
}

// TestAngenommenerVorschlagIstNichtVeraltet: "stale" and "in conflict" are
// questions about an OPEN proposal.
//
// After the accept, the running version holds exactly the files of the
// proposal — so the comparison against its base version reliably reports
// a change, and precisely on the files the accept itself
// wrote. That way the archive showed a red conflict
// note behind every successful accept.
func TestAngenommenerVorschlagIstNichtVeraltet(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")

	item := vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})
	entschieden := admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)
	if entschieden["stale"] == true || entschieden["conflicts"] != nil {
		t.Fatalf("die Antwort auf die Annahme darf keinen Konflikt melden: %v", entschieden)
	}

	archiv := admin.expectList(http.MethodGet, "/api/v1/improvements?status=accepted", nil, http.StatusOK)
	if len(archiv) != 1 {
		t.Fatalf("genau ein angenommener Punkt erwartet: %v", archiv)
	}
	if archiv[0]["stale"] == true || archiv[0]["conflicts"] != nil {
		t.Fatalf("im Archiv steht ein Konflikt hinter der erfolgreichen Annahme: %v", archiv[0])
	}
}

// TestDoctorBehaeltSeinenNamen: covey Doctor is called "covey Doctor" on
// every instance — the name belongs to the platform, not the organisation.
//
// It may read every colleague and propose changes for them. Someone who gave
// it an unremarkable name would have an agent with exactly these rights,
// which nobody in the org chart recognises as such — so the block sits in the
// server and not in the input field.
func TestDoctorBehaeltSeinenNamen(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Already at creation: the reserved slug brings the name with it.
	doctor, err := s.registry.Create(t.Context(), s.orgID, agents.DoctorSlug, "Karl Heinz", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if doctor.DisplayName != agents.DoctorName {
		t.Fatalf("der reservierte Slug muss den Namen mitbringen: %q", doctor.DisplayName)
	}

	admin.expect(http.MethodPatch, "/api/v1/agents/"+doctor.ID.String()+"/name",
		map[string]any{"display_name": "Karl Heinz"}, http.StatusConflict)
	admin.expect(http.MethodPatch, "/api/v1/agents/"+doctor.ID.String()+"/slug",
		map[string]any{"slug": "karl-heinz"}, http.StatusConflict)

	// Renaming to the same name stays allowed — otherwise every
	// idempotent call would be an error.
	admin.expect(http.MethodPatch, "/api/v1/agents/"+doctor.ID.String()+"/name",
		map[string]any{"display_name": agents.DoctorName}, http.StatusOK)

	// Every other agent stays freely nameable.
	normal := s.newSupportAgent("kollege")
	admin.expect(http.MethodPatch, "/api/v1/agents/"+normal.ID.String()+"/name",
		map[string]any{"display_name": "Karl Heinz"}, http.StatusOK)
}
