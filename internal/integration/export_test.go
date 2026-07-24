package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestExportImportRoundtrip prüft das Ziel „Config von Bots komplett
// exportierbar und importierbar": ein voll konfigurierter Agent wird als
// JSON-Bundle exportiert, unter neuem Slug importiert, und der Import trägt
// alles wieder ein — Stammdaten, Config-Dateien, Board, Tools, Egress,
// Guard-Rails, Secret-Zuweisungen (Namen, nie Werte) und Webhook (frisches
// Token).
func TestExportImportRoundtrip(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	// --- Quell-Agent voll konfigurieren. ---
	created := c.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "exportling", "display_name": "Exportling", "runtime": "mock"}, http.StatusCreated)
	id := created["id"].(string)

	c.expect(http.MethodPatch, "/api/v1/agents/"+id+"/model", map[string]string{"model": "claude-test-1"}, http.StatusOK)
	c.expect(http.MethodPatch, "/api/v1/agents/"+id+"/max-turns", map[string]int{"max_turns": 7}, http.StatusOK)
	c.expect(http.MethodPost, "/api/v1/agents/"+id+"/budget", map[string]float64{"budget_usd": 12.5}, http.StatusOK)
	webhook := c.expect(http.MethodPost, "/api/v1/agents/"+id+"/webhook", nil, http.StatusOK)
	srcToken := webhook["token"].(string)

	c.expect(http.MethodPost, "/api/v1/agents/"+id+"/stages",
		map[string]string{"name": "Review", "color": "var(--text-accent)"}, http.StatusCreated)

	tpl := c.expect(http.MethodPost, "/api/v1/egress/templates",
		map[string]string{"name": "beispiel-apis", "description": "Test-APIs"}, http.StatusOK)
	tplID := tpl["id"].(string)
	c.expect(http.MethodPost, "/api/v1/egress/templates/"+tplID+"/hosts",
		map[string]string{"Pattern": "api.example.com"}, http.StatusOK)
	c.expect(http.MethodPut, "/api/v1/agents/"+id+"/egress/templates/"+tplID, nil, http.StatusOK)
	c.expect(http.MethodPost, "/api/v1/agents/"+id+"/egress/hosts",
		map[string]string{"Pattern": "solo.example.com", "Note": "einzeln"}, http.StatusOK)

	c.expect(http.MethodPut, "/api/v1/agents/"+id+"/config", map[string]any{"files": map[string]string{
		"SOUL.md":      "# Exportling\n\n## Rolle\nTest-Bot für den Export.",
		"HEARTBEAT.md": "- alle: 30m titel: Posteingang aufgabe: Sichte neue Tickets.",
		"ACCESS.md":    "- system: zammad   scope: read,write   tools: reply, close",
	}}, http.StatusOK)

	c.expect(http.MethodPost, "/api/v1/guardrails", map[string]any{
		"scope_level": "agent", "agent_id": id,
		"rule_type": "require_approval", "pattern": "zammad:reply_external",
	}, http.StatusCreated)

	c.expect(http.MethodPut, "/api/v1/secrets/zammad_token", map[string]string{"value": "org-geheim-1234567890"}, http.StatusOK)
	c.expect(http.MethodPut, "/api/v1/secrets/zammad_token/agents/"+id, nil, http.StatusOK)
	c.expect(http.MethodPut, "/api/v1/agents/"+id+"/secrets/private_key", map[string]string{"value": "agent-geheim-1234567890"}, http.StatusOK)

	// --- Export. ---
	resp := c.do(http.MethodGet, "/api/v1/agents/"+id+"/export", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: HTTP %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "exportling-config.json") {
		t.Errorf("Content-Disposition fehlt/falsch: %q", cd)
	}
	var bundle map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	raw, _ := json.Marshal(bundle)
	if strings.Contains(string(raw), "geheim-1234567890") || strings.Contains(string(raw), srcToken) {
		t.Fatal("bundle enthält Secret-Werte oder das Webhook-Token")
	}
	if bundle["kind"] != "covey.agent-config" {
		t.Fatalf("kind = %v", bundle["kind"])
	}
	files := bundle["files"].(map[string]any)
	if !strings.Contains(files["ACCESS.md"].(string), "tools: close, reply") &&
		!strings.Contains(files["ACCESS.md"].(string), "tools: reply, close") {
		t.Fatalf("ACCESS.md ohne Tool-Allowlist: %q", files["ACCESS.md"])
	}
	if !strings.Contains(files["EGRESS.md"].(string), "templates: beispiel-apis") ||
		!strings.Contains(files["EGRESS.md"].(string), "solo.example.com") {
		t.Fatalf("EGRESS.md unvollständig: %q", files["EGRESS.md"])
	}
	sec := bundle["secrets"].(map[string]any)
	if got, _ := json.Marshal(sec["org_keys"]); string(got) != `["zammad_token"]` {
		t.Fatalf("org_keys = %s", got)
	}
	if got, _ := json.Marshal(sec["agent_keys"]); string(got) != `["private_key"]` {
		t.Fatalf("agent_keys = %s", got)
	}
	if n := len(bundle["guardrails"].([]any)); n != 1 {
		t.Fatalf("guardrails im Bundle: %d", n)
	}

	// --- Import: Slug-Kollision, dann Kopie unter neuem Slug. ---
	c.expect(http.MethodPost, "/api/v1/agents/import", bundle, http.StatusConflict)
	imported := c.expect(http.MethodPost, "/api/v1/agents/import?slug=importling", bundle, http.StatusCreated)
	na := imported["agent"].(map[string]any)
	nid := na["id"].(string)
	if na["slug"] != "importling" || na["model"] != "claude-test-1" ||
		na["max_turns"].(float64) != 7 || na["budget_usd"].(float64) != 12.5 {
		t.Fatalf("importierter Agent unvollständig: %v", na)
	}
	warnJSON, _ := json.Marshal(imported["warnings"])
	if !strings.Contains(string(warnJSON), "private_key") {
		t.Fatalf("warnung zum agent-eigenen Secret fehlt: %s", warnJSON)
	}

	// --- Der Import hat alles wieder eingetragen. ---
	cfg := c.expect(http.MethodGet, "/api/v1/agents/"+nid+"/config", nil, http.StatusOK)
	nfiles := cfg["files"].(map[string]any)
	if nfiles["SOUL.md"] != files["SOUL.md"] {
		t.Fatalf("SOUL.md verändert: %q", nfiles["SOUL.md"])
	}
	if !strings.Contains(nfiles["EGRESS.md"].(string), "templates: beispiel-apis") ||
		!strings.Contains(nfiles["EGRESS.md"].(string), "solo.example.com") {
		t.Fatalf("EGRESS.md nicht übernommen: %q", nfiles["EGRESS.md"])
	}

	var count int
	mustScan := func(query string, args ...any) int {
		t.Helper()
		if err := s.pool.QueryRow(t.Context(), query, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if n := mustScan("SELECT COUNT(*) FROM agent_target_tools WHERE agent_id=$1 AND system='zammad'", nid); n != 2 {
		t.Errorf("tool-allowlist: %d Einträge (erwartet 2)", n)
	}
	if n := mustScan("SELECT COUNT(*) FROM agent_heartbeats WHERE agent_id=$1 AND name='Posteingang'", nid); n != 1 {
		t.Errorf("heartbeat nicht materialisiert")
	}
	if n := mustScan("SELECT COUNT(*) FROM agent_stages WHERE agent_id=$1", nid); n != 4 {
		t.Errorf("stages: %d (erwartet 4: 3 Defaults + Review)", n)
	}
	if n := mustScan("SELECT COUNT(*) FROM guardrails WHERE agent_id=$1 AND rule_type='require_approval'", nid); n != 1 {
		t.Errorf("agent-guardrail nicht importiert")
	}
	if n := mustScan("SELECT COUNT(*) FROM secret_assignments WHERE agent_id=$1 AND key='zammad_token'", nid); n != 1 {
		t.Errorf("org-secret-zuweisung nicht importiert")
	}
	nwebhook := c.expect(http.MethodGet, "/api/v1/agents/"+nid+"/webhook", nil, http.StatusOK)
	if nwebhook["enabled"] != true {
		t.Error("webhook nicht aktiviert")
	} else if nwebhook["token"] == srcToken {
		t.Error("webhook-token wurde kopiert statt neu erzeugt")
	}
}

// TestImportConfigOverwrite prüft das Überschreiben eines BESTEHENDEN Agenten
// aus einem Bundle, bei dem NUR die Config-Dateien übernommen werden: die
// Config des Ziels wird die des Bundles, seine Stammdaten (Slug, Name, Model)
// bleiben unangetastet.
func TestImportConfigOverwrite(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	// Quell-Agent mit einer bestimmten Config, dann exportieren.
	src := c.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "quelle", "display_name": "Quelle", "runtime": "mock"}, http.StatusCreated)
	sid := src["id"].(string)
	c.expect(http.MethodPut, "/api/v1/agents/"+sid+"/config", map[string]any{"files": map[string]string{
		"SOUL.md":      "# Quelle\n\nGeteilte Basis-Config.",
		"HEARTBEAT.md": "- alle: 10m titel: Ping aufgabe: Prüfe das Zielsystem.",
	}}, http.StatusOK)

	resp := c.do(http.MethodGet, "/api/v1/agents/"+sid+"/export", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: HTTP %d", resp.StatusCode)
	}
	var bundle map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Ziel-Agent mit ANDERER Config und eigenen Stammdaten.
	tgt := c.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "ziel", "display_name": "Ziel", "runtime": "mock"}, http.StatusCreated)
	tid := tgt["id"].(string)
	c.expect(http.MethodPatch, "/api/v1/agents/"+tid+"/model", map[string]string{"model": "claude-test-9"}, http.StatusOK)
	c.expect(http.MethodPut, "/api/v1/agents/"+tid+"/config", map[string]any{"files": map[string]string{
		"SOUL.md": "# Ziel\n\nAlte Config, wird überschrieben.",
	}}, http.StatusOK)

	// Bundle-Config-Import auf den bestehenden Ziel-Agenten.
	c.expect(http.MethodPost, "/api/v1/agents/"+tid+"/config/import", bundle, http.StatusOK)

	// Config des Ziels ist jetzt die des Bundles ...
	cfg := c.expect(http.MethodGet, "/api/v1/agents/"+tid+"/config", nil, http.StatusOK)
	nfiles := cfg["files"].(map[string]any)
	if nfiles["SOUL.md"] != "# Quelle\n\nGeteilte Basis-Config." {
		t.Fatalf("SOUL.md nicht überschrieben: %q", nfiles["SOUL.md"])
	}
	// ... aber die Stammdaten des Ziels bleiben unangetastet (nur Config übernommen).
	after := c.expect(http.MethodGet, "/api/v1/agents/"+tid, nil, http.StatusOK)
	if after["slug"] != "ziel" || after["display_name"] != "Ziel" || after["model"] != "claude-test-9" {
		t.Fatalf("Stammdaten des Ziels wurden verändert: %v", after)
	}

	// Heartbeat aus dem Bundle wurde materialisiert (Config-Write-Through).
	var count int
	if err := s.pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM agent_heartbeats WHERE agent_id=$1 AND name='Ping'", tid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("heartbeat aus dem Bundle nicht materialisiert: %d", count)
	}

	// Fehlerfälle: unbekannter Agent → 404, kaputtes Bundle → 400.
	c.expect(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000000/config/import",
		bundle, http.StatusNotFound)
	c.expect(http.MethodPost, "/api/v1/agents/"+tid+"/config/import",
		map[string]any{"kind": "falsch", "version": 1, "files": map[string]string{"SOUL.md": "x"}}, http.StatusBadRequest)
}
