package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/agents"
	identbuiltin "covey/internal/identity/builtin"
)

// vorschlag legt einen Vorschlag an, wie ihn später die Aktion
// covey/propose_agent_config schreibt: der Absender ist ein Agent, die
// Basisversion setzt die Plattform.
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

// TestVorschlagLiegtUndLaeuftNicht ist die Eigenschaft, auf der spec/21 steht:
// der Betriebsingenieur schlägt vor, er entscheidet nicht. Ein offener
// Vorschlag erreicht den bewerteten Agenten auf keinem Weg — er ist keine
// Version, also gibt es keinen Zusammenbau, in den er geraten könnte.
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

	// Die laufende Config kennt ihn nicht — weder als Datei noch im Prompt.
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

	// Die Liste zeigt ihn, mit Diff und ohne Konflikt.
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

	// Annehmen: eine neue Version auf dem normalen Schreibweg, mit dem
	// Menschen als Urheber — und die vorher unangetasteten Dateien stehen noch.
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

	// Entschieden wird einmal. Der zweite Klick ist kein zweiter Beschluss.
	admin.expect(http.MethodPost, "/api/v1/improvements/"+item.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusConflict)
}

// TestVorschlagAufAccessBrauchtSecurity: die Tiefe entscheidet, wer annehmen
// darf. ACCESS.md und EGRESS.md sind die Textansicht auf Zustand, dessen
// Schreibweg bei platform_admin/security liegt (spec/02) — ein Review-Dialog,
// der alles durchlässt, weil der Vorschlag harmlos aussah, verschöbe die
// Zugriffsentscheidung zu dem, der zuerst geklickt hat.
func TestVorschlagAufAccessBrauchtSecurity(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	hash, _ := identbuiltin.HashPassword("owner-passwort")
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'owner@test.local','Teamleiter',$3,'agent_owner')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}
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

	// Der Teamleiter darf den Playbook-Vorschlag annehmen.
	owner.expect(http.MethodPost, "/api/v1/improvements/"+harmlos.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)
	// Den, der den Zugang weitet, nicht.
	owner.expect(http.MethodPost, "/api/v1/improvements/"+weitend.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusForbidden)
	// Ablehnen darf er ihn: das nimmt nichts weg.
	abgelehnt := owner.expect(http.MethodPost, "/api/v1/improvements/"+weitend.ID.String()+"/decide",
		map[string]any{"accept": false, "note": "Nicht nötig."}, http.StatusOK)
	if abgelehnt["status"] != "rejected" || abgelehnt["decision_note"] != "Nicht nötig." {
		t.Fatalf("der Grund der Ablehnung muss stehen bleiben: %v", abgelehnt)
	}

	// Und der abgelehnte Vorschlag bleibt lesbar — er ist das Nützlichste,
	// was jemand liest, der den Betriebsingenieur selbst überprüft.
	rejected := admin.expectList(http.MethodGet, "/api/v1/improvements?status=rejected", nil, http.StatusOK)
	if len(rejected) != 1 {
		t.Fatalf("abgelehnte Punkte bleiben stehen: %v", rejected)
	}
}

// TestVorschlagUeberschreibtKeineFremdeÄnderung: ein Vorschlag ist ein Diff
// gegen eine Basis. Wird dieselbe Datei zwischenzeitlich von Hand geändert,
// wird er nicht still angewandt — derselbe Konflikt wie bei einem Pull
// Request. Eine Änderung an einer ANDEREN Datei macht ihn nur veraltet.
func TestVorschlagUeberschreibtKeineFremdeÄnderung(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ziel := s.newSupportAgent("kollege")
	autor := s.newSupportAgent("betrieb")

	item := vorschlag(t, s, ziel, autor, map[string]string{"SOUL.md": "# Support\n\nGeschaerft."})
	nebenbei := vorschlag(t, s, ziel, autor, map[string]string{"PLAYBOOKS.md": "## Vorgehen\n\nErst lesen."})

	// Ein Mensch bearbeitet die SOUL.md.
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
	// Der veraltete, aber konfliktfreie Vorschlag geht durch.
	admin.expect(http.MethodPost, "/api/v1/improvements/"+nebenbei.ID.String()+"/decide",
		map[string]any{"accept": true}, http.StatusOK)
}

// TestSelbstvorschlagLiegtWieJederAndere: ein Agent darf seine EIGENE Config
// vorschlagen — das ist der offene Punkt aus spec/20, und er ist ungefährlich,
// weil von hier nichts läuft. Bis ein Mensch ihn annimmt, ändert er nichts.
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
