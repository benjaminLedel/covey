package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/examples"
	"covey/internal/backlog"
	identbuiltin "covey/internal/identity/builtin"
)

// skillView deckt alle drei Antwortformen ab: Bibliotheks-Liste (ohne Dateien),
// einzelner Skill (mit Dateien) und die aufgelöste Agenten-Sicht (zusätzlich
// origin).
type skillView struct {
	ID          string   `json:"id"`
	AgentID     string   `json:"agent_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	AssignedTo  []string `json:"assigned_to"`
	Origin      string   `json:"origin"`
	Files       []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
}

func (v skillView) file(path string) string {
	for _, f := range v.Files {
		if f.Path == path {
			return f.Content
		}
	}
	return ""
}

func getSkillList(t *testing.T, c *apiClient, path string) []skillView {
	t.Helper()
	resp := c.do(http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	var out []skillView
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return out
}

func getSkill(t *testing.T, c *apiClient, path string) skillView {
	t.Helper()
	resp := c.do(http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	var out skillView
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return out
}

func skillFiles(files ...[2]string) []map[string]string {
	out := make([]map[string]string, 0, len(files))
	for _, f := range files {
		out = append(out, map[string]string{"path": f[0], "content": f[1]})
	}
	return out
}

// TestSkillsAPI prüft die HTTP-Oberfläche der Agenten-Skills gegen echtes
// Postgres: Anlegen mit Frontmatter, die Opt-in-Regel der Bibliothek (ohne
// Verlinkung erreicht ein Skill niemanden), den Vorrang agent-eigener Skills,
// die Eingabeprüfungen und die Rollen-Rechte.
func TestSkillsAPI(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	hash, _ := identbuiltin.HashPassword("auditor-passwort")
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'skill-auditor@test.local','Auditor',$3,'auditor')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	auditor := login(t, s, "skill-auditor@test.local", "auditor-passwort")

	alice := s.newSupportAgent("alice")
	bob := s.newSupportAgent("bob")

	// Die ältere Route /api/v1/skills/covey-agent.zip (der Claude-Code-Skill zum
	// Herunterladen) liegt unter demselben Präfix wie /api/v1/skills/{id} und
	// muss ihm als literales Segment vorgehen — sonst wäre sie plötzlich eine
	// „ungültige id".
	zip := admin.do(http.MethodGet, "/api/v1/skills/covey-agent.zip", nil)
	zip.Body.Close()
	if zip.StatusCode != http.StatusOK || zip.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("Skill-Download muss erreichbar bleiben: HTTP %d (%s)",
			zip.StatusCode, zip.Header.Get("Content-Type"))
	}

	// Anlegen: Name und Beschreibung dürfen aus dem Frontmatter der SKILL.md
	// kommen — so lässt sich eine fertige Datei einfach hineinkopieren.
	created := admin.expect(http.MethodPost, "/api/v1/skills", map[string]any{
		"files": skillFiles(
			[2]string{"SKILL.md", "---\nname: deploy\ndescription: \"Nutze dies, wenn: ein Release ansteht\"\n---\n\n# Deploy\n\nSchritt 1.\n"},
			[2]string{"checkliste.md", "- [ ] Tests grün\n"},
		),
	}, http.StatusCreated)
	deployID, _ := created["id"].(string)
	if created["name"] != "deploy" || created["description"] != "Nutze dies, wenn: ein Release ansteht" {
		t.Fatalf("Frontmatter muss Name und Beschreibung füllen: %+v", created)
	}

	// Gespeichert wird der Rumpf OHNE Frontmatter — die Beschreibung steht in
	// der Spalte, sonst könnten sich beide widersprechen.
	full := getSkill(t, admin, "/api/v1/skills/"+deployID)
	if body := full.file("SKILL.md"); !strings.HasPrefix(body, "# Deploy") {
		t.Fatalf("Frontmatter muss abgeschnitten sein, got %q", body)
	}
	if full.file("checkliste.md") == "" {
		t.Fatal("Zusatzdatei fehlt — genau davon lebt das Feature")
	}

	// Derselbe Name auf derselben Ebene ersetzt nichts still.
	admin.expect(http.MethodPost, "/api/v1/skills", map[string]any{
		"name": "deploy", "description": "Zweiter Versuch",
		"files": skillFiles([2]string{"SKILL.md", "# X\n"}),
	}, http.StatusConflict)

	// Eingabeprüfungen sind 400, nicht 500: Sie sagen dem Aufrufer, was fehlt.
	for _, bad := range []struct {
		was  string
		body map[string]any
	}{
		{"Großbuchstaben im Namen", map[string]any{"name": "Deploy", "description": "d",
			"files": skillFiles([2]string{"SKILL.md", "x"})}},
		{"Pfad bricht aus dem Skill aus", map[string]any{"name": "traversal", "description": "d",
			"files": skillFiles([2]string{"SKILL.md", "x"}, [2]string{"../../.claude/settings.json", "{}"})}},
		{"SKILL.md fehlt", map[string]any{"name": "leer", "description": "d",
			"files": skillFiles([2]string{"referenz.md", "x"})}},
		{"Beschreibung fehlt", map[string]any{"name": "ohne-desc",
			"files": skillFiles([2]string{"SKILL.md", "x"})}},
	} {
		admin.expect(http.MethodPost, "/api/v1/skills", bad.body, http.StatusBadRequest)
		if list := getSkillList(t, admin, "/api/v1/skills"); len(list) != 1 {
			t.Fatalf("%s darf nichts anlegen, Bibliothek: %+v", bad.was, list)
		}
	}

	// Opt-in: ein Bibliotheks-Skill ohne Verlinkung erreicht niemanden.
	if list := getSkillList(t, admin, "/api/v1/skills"); len(list) != 1 || len(list[0].AssignedTo) != 0 {
		t.Fatalf("Bibliothek erwartet einen unverlinkten Eintrag: %+v", list)
	}
	if own := getSkillList(t, admin, "/api/v1/agents/"+alice.ID.String()+"/skills"); len(own) != 0 {
		t.Fatalf("ohne Verlinkung darf alice nichts bekommen: %+v", own)
	}

	// Verlinken: nur alice bekommt den Skill, bob nicht.
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID+"/agents/"+alice.ID.String(), nil, http.StatusOK)
	aliceSkills := getSkillList(t, admin, "/api/v1/agents/"+alice.ID.String()+"/skills")
	if len(aliceSkills) != 1 || aliceSkills[0].Origin != "library" || aliceSkills[0].file("checkliste.md") == "" {
		t.Fatalf("alice erwartet den verlinkten Bibliotheks-Skill samt Dateien: %+v", aliceSkills)
	}
	if bobSkills := getSkillList(t, admin, "/api/v1/agents/"+bob.ID.String()+"/skills"); len(bobSkills) != 0 {
		t.Fatalf("bob darf ohne Verlinkung nichts bekommen: %+v", bobSkills)
	}
	if list := getSkillList(t, admin, "/api/v1/skills"); len(list[0].AssignedTo) != 1 ||
		list[0].AssignedTo[0] != alice.ID.String() {
		t.Fatalf("Bibliothek muss die Verlinkung zeigen: %+v", list)
	}

	// Agent-eigener Skill gleichen Namens: erlaubt und mit Vorrang.
	bobOwn := admin.expect(http.MethodPost, "/api/v1/agents/"+bob.ID.String()+"/skills", map[string]any{
		"name": "deploy", "description": "Bobs eigener Deploy-Weg",
		"files": skillFiles([2]string{"SKILL.md", "# Bobs Deploy\n"}),
	}, http.StatusCreated)
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID+"/agents/"+bob.ID.String(), nil, http.StatusOK)
	bobSkills := getSkillList(t, admin, "/api/v1/agents/"+bob.ID.String()+"/skills")
	if len(bobSkills) != 1 || bobSkills[0].Origin != "agent" ||
		!strings.Contains(bobSkills[0].file("SKILL.md"), "Bobs Deploy") {
		t.Fatalf("bei Namensgleichheit muss der eigene Skill gewinnen: %+v", bobSkills)
	}

	// Agent-eigene Skills lassen sich nicht verlinken — sie gehören schon jemandem.
	admin.expect(http.MethodPut, "/api/v1/skills/"+bobOwn["id"].(string)+"/agents/"+alice.ID.String(),
		nil, http.StatusBadRequest)

	// Unbekannter Agent bzw. Skill: 404, kein 500.
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID+"/agents/"+uuid.NewString(), nil, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/skills/"+uuid.NewString(), nil, http.StatusNotFound)

	// Ersetzen tauscht den ganzen Dateisatz aus (weggelassene Datei ist weg).
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID, map[string]any{
		"description": "Nutze dies beim Release",
		"files":       skillFiles([2]string{"SKILL.md", "# Deploy\n\nNeu.\n"}),
	}, http.StatusOK)
	full = getSkill(t, admin, "/api/v1/skills/"+deployID)
	if len(full.Files) != 1 || full.Description != "Nutze dies beim Release" {
		t.Fatalf("PUT muss Beschreibung und Dateisatz ersetzen: %+v", full)
	}

	// Umbenennen wird abgelehnt: Der Name ist Verzeichnis und /slash-command.
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID, map[string]any{
		"name": "deploy-neu", "description": "x",
		"files": skillFiles([2]string{"SKILL.md", "# X\n"}),
	}, http.StatusConflict)

	// Rollen: Auditor liest, ändert aber nichts (spec/09).
	auditor.expect(http.MethodGet, "/api/v1/skills", nil, http.StatusOK)
	auditor.expect(http.MethodGet, "/api/v1/agents/"+alice.ID.String()+"/skills", nil, http.StatusOK)
	auditor.expect(http.MethodPost, "/api/v1/skills", map[string]any{"name": "x", "description": "d",
		"files": skillFiles([2]string{"SKILL.md", "x"})}, http.StatusForbidden)
	auditor.expect(http.MethodPut, "/api/v1/skills/"+deployID+"/agents/"+alice.ID.String(), nil, http.StatusForbidden)
	auditor.expect(http.MethodDelete, "/api/v1/skills/"+deployID, nil, http.StatusForbidden)

	// Entziehen und Löschen wirken auf die Agenten-Sicht durch.
	admin.expect(http.MethodDelete, "/api/v1/skills/"+deployID+"/agents/"+alice.ID.String(), nil, http.StatusOK)
	if own := getSkillList(t, admin, "/api/v1/agents/"+alice.ID.String()+"/skills"); len(own) != 0 {
		t.Fatalf("nach dem Entziehen darf alice nichts mehr bekommen: %+v", own)
	}
	admin.expect(http.MethodDelete, "/api/v1/skills/"+deployID, nil, http.StatusOK)
	if list := getSkillList(t, admin, "/api/v1/skills"); len(list) != 0 {
		t.Fatalf("Bibliothek muss leer sein: %+v", list)
	}
	// Bobs eigener Skill bleibt davon unberührt.
	if bobSkills = getSkillList(t, admin, "/api/v1/agents/"+bob.ID.String()+"/skills"); len(bobSkills) != 1 {
		t.Fatalf("agent-eigener Skill darf nicht mitgelöscht werden: %+v", bobSkills)
	}
}

// TestDeliveryLeadSkills hält die Umstellung des mitgelieferten Delivery Leads
// fest: Seine Prozeduren liegen als Skills im Bundle, nicht mehr in
// PLAYBOOKS.md — und das ausgelieferte Bundle muss den Weg durch den Import
// nehmen, dessen Skill-Prüfung streng ist.
//
// Dass jeder Skill auch irgendwo genannt wird, prüft examples.TestBuiltinSkills
// für alle Vorlagen ohne Datenbank.
func TestDeliveryLeadSkills(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	var bundle json.RawMessage
	for _, b := range examples.Builtins() {
		if b.Key == "builtin:delivery-lead" {
			bundle = b.Bundle
		}
	}
	if bundle == nil {
		t.Fatal("mitgelieferte Delivery-Lead-Vorlage nicht gefunden")
	}
	imported := c.expect(http.MethodPost, "/api/v1/agents/import?slug=lead-probe", bundle, http.StatusCreated)
	nid := imported["agent"].(map[string]any)["id"].(string)

	got := getSkillList(t, c, "/api/v1/agents/"+nid+"/skills")
	want := map[string]bool{"ruecklaeufer": false, "arbeit-vergeben": false,
		"ticket-aufbereiten": false, "tagesbericht": false}
	for _, sk := range got {
		if _, ok := want[sk.Name]; !ok {
			t.Errorf("unerwarteter Skill %q", sk.Name)
			continue
		}
		want[sk.Name] = true
		if sk.Origin != "agent" {
			t.Errorf("%s: origin=%q (die Lead-Prozeduren gehören ihm allein)", sk.Name, sk.Origin)
		}
		if len(sk.file("SKILL.md")) < 200 {
			t.Errorf("%s: SKILL.md zu dünn (%d Zeichen)", sk.Name, len(sk.file("SKILL.md")))
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("Skill %q fehlt am importierten Agenten", name)
		}
	}
}

// TestSkillsMaterialisierung ist der Durchstich: Was über die API in der
// Bibliothek liegt und einem Agenten verlinkt ist, muss beim Lauf in seinem
// Home unter .claude/skills/<name>/ stehen — dort, wo Claude Code persönliche
// Skills sucht. Und der Entzug muss ebenso durchschlagen: Das Home überlebt den
// Lauf, ein abgehängter Skill bliebe sonst für immer wirksam.
func TestSkillsMaterialisierung(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("skill-laeufer")

	created := admin.expect(http.MethodPost, "/api/v1/skills", map[string]any{
		"name": "recherche", "description": "Nutze dies, wenn: eine Quelle geprüft werden muss",
		"files": skillFiles(
			[2]string{"SKILL.md", "# Recherche\n\nSiehe quellen.md.\n"},
			[2]string{"quellen.md", "- Handelsregister\n"},
		),
	}, http.StatusCreated)
	skillID := created["id"].(string)
	admin.expect(http.MethodPut, "/api/v1/skills/"+skillID+"/agents/"+agent.ID.String(), nil, http.StatusOK)

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Erster Lauf", "[mock:result ok]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "erste aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	dir := filepath.Join(s.homeBase, agent.ID.String(), ".claude", "skills", "recherche")
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md muss im Home liegen: %v", err)
	}
	// Der Frontmatter entsteht erst beim Schreiben — mit quotierter Beschreibung,
	// sonst läse Claude Code den Doppelpunkt als YAML-Map.
	if !strings.HasPrefix(string(raw), "---\nname: recherche\n") ||
		!strings.Contains(string(raw), `description: "Nutze dies, wenn: eine Quelle geprüft werden muss"`) ||
		!strings.Contains(string(raw), "# Recherche") {
		t.Fatalf("materialisierte SKILL.md unerwartet:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "quellen.md")); err != nil {
		t.Fatalf("Zusatzdatei muss mitkommen: %v", err)
	}

	// Entzug: Verlinkung lösen, nächster Lauf räumt das Verzeichnis ab.
	admin.expect(http.MethodDelete, "/api/v1/skills/"+skillID+"/agents/"+agent.ID.String(), nil, http.StatusOK)
	task2, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Zweiter Lauf", "[mock:result ok]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "zweite aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task2.ID) == backlog.StateDone
	})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("entzogener Skill muss aus dem Home verschwinden: %v", err)
	}
}
