package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	identbuiltin "covey/internal/identity/builtin"
)

// apiClient ist ein eingeloggter HTTP-Client (Session-Cookie).
type apiClient struct {
	t    *testing.T
	base string
	http *http.Client
}

func login(t *testing.T, s *stack, email, password string) *apiClient {
	t.Helper()
	jar := newJar()
	c := &apiClient{t: t, base: s.http.URL, http: &http.Client{Jar: jar}}
	resp := c.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": email, "password": password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login fehlgeschlagen: HTTP %d", resp.StatusCode)
	}
	resp.Body.Close()
	return c
}

func (c *apiClient) do(method, path string, body any) *http.Response {
	c.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func (c *apiClient) expect(method, path string, body any, wantStatus int) map[string]any {
	c.t.Helper()
	resp := c.do(method, path, body)
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		c.t.Fatalf("%s %s: HTTP %d (erwartet %d): %s", method, path, resp.StatusCode, wantStatus, buf.String())
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func TestAPIAndRBAC(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// Zweitnutzer mit read-only-Rolle (Auditor).
	hash, _ := identbuiltin.HashPassword("auditor-passwort")
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'auditor@test.local','Auditor',$3,'auditor')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}

	// Health ohne Auth.
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(s.http.URL + path)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s muss ohne Auth ok sein: %v %d", path, err, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// API ohne Login → 401.
	resp, _ := http.Get(s.http.URL + "/api/v1/agents")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ohne login erwartet 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Falsches Passwort → 401.
	bad := &apiClient{t: t, base: s.http.URL, http: &http.Client{Jar: newJar()}}
	r := bad.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@test.local", "password": "falsch"})
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("falsches passwort erwartet 401, got %d", r.StatusCode)
	}
	r.Body.Close()

	admin := login(t, s, "admin@test.local", "admin-passwort")
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")

	// Admin legt Agent + Config + Aufgabe an (M0: sichtbar in der Liste).
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "api-agent", "display_name": "API-Agent", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)

	admin.expect(http.MethodPut, "/api/v1/agents/"+agentID+"/config",
		map[string]any{"files": map[string]string{"SOUL.md": "# X\n\n## Rolle\nTest."}}, http.StatusOK)

	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/tasks",
		map[string]any{"title": "Test", "body": "[mock:result ok]"}, http.StatusCreated)

	// Auditor: lesen ja, schreiben nein (rollen-gescopte Sichten, spec/09).
	auditor.expect(http.MethodGet, "/api/v1/agents/"+agentID+"/backlog", nil, http.StatusOK)
	auditor.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "x", "display_name": "X"}, http.StatusForbidden)
	auditor.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/kill", nil, http.StatusForbidden)
	auditor.expect(http.MethodPut, "/api/v1/secrets/foo", map[string]string{"value": "bar"}, http.StatusForbidden)

	// Secrets sind write-only: PUT ok, Liste zeigt nur Namen plus begrenztes Präfix.
	admin.expect(http.MethodPut, "/api/v1/secrets/zammad_token", map[string]string{"value": "geheim"}, http.StatusOK)
	respKeys := admin.do(http.MethodGet, "/api/v1/secrets", nil)
	var keys []struct {
		Key    string `json:"key"`
		Prefix string `json:"prefix"`
	}
	json.NewDecoder(respKeys.Body).Decode(&keys)
	respKeys.Body.Close()
	if len(keys) != 1 || keys[0].Key != "zammad_token" {
		t.Fatalf("secret-keys erwartet [zammad_token], got %v", keys)
	}
	// "geheim" (6 Zeichen ≤ 12) → vollständig maskiert, kein Präfix.
	if keys[0].Prefix != "" {
		t.Fatalf("kurzes secret darf kein Präfix zeigen, got %q", keys[0].Prefix)
	}

	// Custom-Stages: anlegen, listen, Aufgabe verschieben (Overlay über state).
	auditor.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/stages",
		map[string]string{"name": "Recherche"}, http.StatusForbidden) // read-only Rolle
	stage := admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/stages",
		map[string]string{"name": "Recherche", "color": "var(--text-accent)"}, http.StatusCreated)
	stageID := stage["id"].(string)

	respStages := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/stages", nil)
	var stages []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(respStages.Body).Decode(&stages)
	respStages.Body.Close()
	// Der Agent startet mit dem Default-Board (Backlog/In Arbeit/Erledigt);
	// die neue Spalte "Recherche" wird hinten angehängt.
	if len(stages) != 4 || stages[len(stages)-1].Name != "Recherche" {
		t.Fatalf("stage-liste erwartet [Default-Board + Recherche], got %v", stages)
	}

	// Eine Aufgabe des Agenten in die Stage schieben.
	respBacklog := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/backlog", nil)
	var backlogTasks []struct {
		ID      string  `json:"id"`
		StageID *string `json:"stage_id"`
	}
	json.NewDecoder(respBacklog.Body).Decode(&backlogTasks)
	respBacklog.Body.Close()
	if len(backlogTasks) == 0 {
		t.Fatal("erwartete mindestens eine Aufgabe im Backlog")
	}
	taskID := backlogTasks[0].ID
	admin.expect(http.MethodPost, "/api/v1/tasks/"+taskID+"/stage",
		map[string]string{"stage_id": stageID}, http.StatusOK)

	respBacklog2 := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/backlog", nil)
	json.NewDecoder(respBacklog2.Body).Decode(&backlogTasks)
	respBacklog2.Body.Close()
	var moved bool
	for _, tk := range backlogTasks {
		if tk.ID == taskID {
			if tk.StageID == nil || *tk.StageID != stageID {
				t.Fatalf("aufgabe sollte in stage %s sein, got %v", stageID, tk.StageID)
			}
			moved = true
		}
	}
	if !moved {
		t.Fatal("verschobene aufgabe nicht wiedergefunden")
	}

	// Stage löschen → Aufgabe fällt auf stage=NULL zurück (kein Verlust).
	admin.expect(http.MethodDelete, "/api/v1/stages/"+stageID, nil, http.StatusNoContent)

	// Guardrail anlegen (Admin hat Security-Rechte im MVP-RBAC).
	admin.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "deny_system", "pattern": "hr*"}, http.StatusCreated)
	auditor.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "deny_system", "pattern": "x"}, http.StatusForbidden)
}

// TestMemoryAdministration: Gedächtnis manuell einspeisen, ändern, löschen —
// Floskeln werden abgelehnt, Schreibrechte nur für manage-Rollen.
func TestMemoryAdministration(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "mem-agent", "display_name": "Mem-Agent", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)

	// Manuell einspeisen; nichtssagende Inhalte → 400.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/memories",
		map[string]string{"content": "Kunde Meier ist Bestandskunde und wird geduzt"}, http.StatusCreated)
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/memories",
		map[string]string{"content": "Keine neuen Erkenntnisse"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/agents/"+uuid.NewString()+"/memories",
		map[string]string{"content": "Wissen für einen Geister-Agenten"}, http.StatusNotFound)

	listMemories := func() []map[string]any {
		t.Helper()
		resp := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/memories", nil)
		defer resp.Body.Close()
		var entries []map[string]any
		json.NewDecoder(resp.Body).Decode(&entries)
		return entries
	}
	entries := listMemories()
	if len(entries) != 1 {
		t.Fatalf("erwartet 1 episode, got %d", len(entries))
	}
	memID := entries[0]["id"].(string)

	// Ändern (inkl. Re-Embedding); Floskel-Änderung → 400, unbekannte ID → 404.
	admin.expect(http.MethodPatch, "/api/v1/memories/"+memID,
		map[string]string{"content": "Kunde Meier wird gesiezt"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/memories/"+memID,
		map[string]string{"content": "nichts neues"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/memories/"+uuid.NewString(),
		map[string]string{"content": "gültiger inhalt ohne ziel"}, http.StatusNotFound)
	if entries = listMemories(); entries[0]["content"] != "Kunde Meier wird gesiezt" {
		t.Fatalf("änderung nicht angekommen: %v", entries[0]["content"])
	}

	// Read-only-Rolle darf lesen, aber nicht schreiben/löschen.
	hash, _ := identbuiltin.HashPassword("auditor-passwort")
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'auditor@test.local','Auditor',$3,'auditor')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")
	auditor.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/memories",
		map[string]string{"content": "Auditor darf das nicht"}, http.StatusForbidden)
	auditor.expect(http.MethodPatch, "/api/v1/memories/"+memID,
		map[string]string{"content": "Auditor darf das nicht"}, http.StatusForbidden)
	auditor.expect(http.MethodDelete, "/api/v1/memories/"+memID, nil, http.StatusForbidden)

	// Löschen; zweites Löschen → 404.
	admin.expect(http.MethodDelete, "/api/v1/memories/"+memID, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/memories/"+memID, nil, http.StatusNotFound)
	if entries = listMemories(); len(entries) != 0 {
		t.Fatalf("gedächtnis sollte leer sein, got %d", len(entries))
	}
}
