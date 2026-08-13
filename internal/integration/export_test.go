package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestExportImportRoundtrip checks the goal "bot config completely exportable
// and importable": a fully configured agent is exported as a JSON bundle,
// imported under a new slug, and the import restores everything — master data,
// config files, board, tools, egress, guard rails, secret assignments (names,
// never values) and webhook (with a fresh token).
func TestExportImportRoundtrip(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	// --- Configure the source agent fully. ---
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
		t.Errorf("Content-Disposition missing/wrong: %q", cd)
	}
	var bundle map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	raw, _ := json.Marshal(bundle)
	if strings.Contains(string(raw), "geheim-1234567890") || strings.Contains(string(raw), srcToken) {
		t.Fatal("the bundle contains secret values or the webhook token")
	}
	if bundle["kind"] != "covey.agent-config" {
		t.Fatalf("kind = %v", bundle["kind"])
	}
	files := bundle["files"].(map[string]any)
	if !strings.Contains(files["ACCESS.md"].(string), "tools: close, reply") &&
		!strings.Contains(files["ACCESS.md"].(string), "tools: reply, close") {
		t.Fatalf("ACCESS.md without a tool allowlist: %q", files["ACCESS.md"])
	}
	if !strings.Contains(files["EGRESS.md"].(string), "templates: beispiel-apis") ||
		!strings.Contains(files["EGRESS.md"].(string), "solo.example.com") {
		t.Fatalf("EGRESS.md incomplete: %q", files["EGRESS.md"])
	}
	sec := bundle["secrets"].(map[string]any)
	if got, _ := json.Marshal(sec["org_keys"]); string(got) != `["zammad_token"]` {
		t.Fatalf("org_keys = %s", got)
	}
	if got, _ := json.Marshal(sec["agent_keys"]); string(got) != `["private_key"]` {
		t.Fatalf("agent_keys = %s", got)
	}
	if n := len(bundle["guardrails"].([]any)); n != 1 {
		t.Fatalf("guardrails in the bundle: %d", n)
	}

	// --- Import: slug collision, then a copy under a new slug. ---
	c.expect(http.MethodPost, "/api/v1/agents/import", bundle, http.StatusConflict)
	imported := c.expect(http.MethodPost, "/api/v1/agents/import?slug=importling", bundle, http.StatusCreated)
	na := imported["agent"].(map[string]any)
	nid := na["id"].(string)
	if na["slug"] != "importling" || na["model"] != "claude-test-1" ||
		na["max_turns"].(float64) != 7 || na["budget_usd"].(float64) != 12.5 {
		t.Fatalf("the imported agent is incomplete: %v", na)
	}
	warnJSON, _ := json.Marshal(imported["warnings"])
	if !strings.Contains(string(warnJSON), "private_key") {
		t.Fatalf("the warning about the agent-owned secret is missing: %s", warnJSON)
	}

	// --- The import has restored everything. ---
	cfg := c.expect(http.MethodGet, "/api/v1/agents/"+nid+"/config", nil, http.StatusOK)
	nfiles := cfg["files"].(map[string]any)
	if nfiles["SOUL.md"] != files["SOUL.md"] {
		t.Fatalf("SOUL.md changed: %q", nfiles["SOUL.md"])
	}
	if !strings.Contains(nfiles["EGRESS.md"].(string), "templates: beispiel-apis") ||
		!strings.Contains(nfiles["EGRESS.md"].(string), "solo.example.com") {
		t.Fatalf("EGRESS.md not carried over: %q", nfiles["EGRESS.md"])
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
		t.Errorf("tool allowlist: %d entries (expected 2)", n)
	}
	if n := mustScan("SELECT COUNT(*) FROM agent_heartbeats WHERE agent_id=$1 AND name='Posteingang'", nid); n != 1 {
		t.Errorf("heartbeat not materialized")
	}
	if n := mustScan("SELECT COUNT(*) FROM agent_stages WHERE agent_id=$1", nid); n != 4 {
		t.Errorf("stages: %d (expected 4: 3 defaults + Review)", n)
	}
	if n := mustScan("SELECT COUNT(*) FROM guardrails WHERE agent_id=$1 AND rule_type='require_approval'", nid); n != 1 {
		t.Errorf("agent guardrail not imported")
	}
	if n := mustScan("SELECT COUNT(*) FROM secret_assignments WHERE agent_id=$1 AND key='zammad_token'", nid); n != 1 {
		t.Errorf("org secret assignment not imported")
	}
	nwebhook := c.expect(http.MethodGet, "/api/v1/agents/"+nid+"/webhook", nil, http.StatusOK)
	if nwebhook["enabled"] != true {
		t.Error("webhook not enabled")
	} else if nwebhook["token"] == srcToken {
		t.Error("the webhook token was copied instead of newly generated")
	}
}

// TestBundleSkills checks the skills in the bundle: they travel along with
// their full content and a note of origin, and an import restores them on the
// receiving side — agent-owned ones as agent-owned, library skills in the
// library and linked to the agent. Without that, people imported an agent whose
// procedures were missing and only noticed during the run.
func TestBundleSkills(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	src := c.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "skill-quelle", "display_name": "Skill-Quelle", "runtime": "mock"}, http.StatusCreated)
	sid := src["id"].(string)
	c.expect(http.MethodPut, "/api/v1/agents/"+sid+"/config", map[string]any{"files": map[string]string{
		"SOUL.md": "# Skill-Quelle\n\n## Rolle\nTrägt Fähigkeiten.",
	}}, http.StatusOK)

	// One skill from the library (linked) and one that belongs to it alone.
	lib := c.expect(http.MethodPost, "/api/v1/skills", map[string]any{
		"name": "gemeinsam", "description": "Nutze dies, wenn: alle es brauchen",
		"files": skillFiles(
			[2]string{"SKILL.md", "# Gemeinsam\n\nSiehe referenz.md.\n"},
			[2]string{"referenz.md", "Tabelle\n"},
		),
	}, http.StatusCreated)
	libID := lib["id"].(string)
	c.expect(http.MethodPut, "/api/v1/skills/"+libID+"/agents/"+sid, nil, http.StatusOK)
	own := c.expect(http.MethodPost, "/api/v1/agents/"+sid+"/skills", map[string]any{
		"name": "eigen", "description": "Nur für diesen Agenten",
		"files": skillFiles([2]string{"SKILL.md", "# Eigen\n"}),
	}, http.StatusCreated)
	ownID := own["id"].(string)

	// --- Export: both skills with full content and origin. ---
	resp := c.do(http.MethodGet, "/api/v1/agents/"+sid+"/export", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: HTTP %d", resp.StatusCode)
	}
	var bundle map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var exported []struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Origin      string            `json:"origin"`
		Files       map[string]string `json:"files"`
	}
	rawSkills, _ := json.Marshal(bundle["skills"])
	if err := json.Unmarshal(rawSkills, &exported); err != nil {
		t.Fatal(err)
	}
	if len(exported) != 2 {
		t.Fatalf("expected two skills: %s", rawSkills)
	}
	byName := map[string]int{}
	for i, sk := range exported {
		byName[sk.Name] = i
	}
	if e := exported[byName["eigen"]]; e.Origin != "agent" {
		t.Fatalf("an agent-owned skill needs origin=agent: %+v", e)
	}
	g := exported[byName["gemeinsam"]]
	if g.Origin != "library" || g.Files["referenz.md"] == "" ||
		!strings.Contains(g.Files["SKILL.md"], "# Gemeinsam") {
		t.Fatalf("library skill exported incompletely: %+v", g)
	}

	// --- Receiving side without these skills: deleting simulates the foreign instance. ---
	c.expect(http.MethodDelete, "/api/v1/skills/"+libID, nil, http.StatusOK)
	c.expect(http.MethodDelete, "/api/v1/skills/"+ownID, nil, http.StatusOK)

	imported := c.expect(http.MethodPost, "/api/v1/agents/import?slug=skill-ziel", bundle, http.StatusCreated)
	nid := imported["agent"].(map[string]any)["id"].(string)

	got := getSkillList(t, c, "/api/v1/agents/"+nid+"/skills")
	if len(got) != 2 {
		t.Fatalf("the imported agent needs both skills: %+v", got)
	}
	origins := map[string]string{}
	for _, sk := range got {
		origins[sk.Name] = sk.Origin
	}
	if origins["eigen"] != "agent" || origins["gemeinsam"] != "library" {
		t.Fatalf("origin wrong after the import: %+v", origins)
	}
	// The library skill is back in the library — not as a copy at the agent,
	// otherwise it would be gone again for the next agent.
	libNow := getSkillList(t, c, "/api/v1/skills")
	if len(libNow) != 1 || libNow[0].Name != "gemeinsam" || len(libNow[0].AssignedTo) != 1 {
		t.Fatalf("library after the import: %+v", libNow)
	}

	// --- Second import: the library skill now exists. It is linked instead of
	// overwritten (it may belong to other agents). ---
	c.expect(http.MethodPut, "/api/v1/skills/"+libNow[0].ID, map[string]any{
		"description": "Örtlich angepasste Fassung",
		"files":       skillFiles([2]string{"SKILL.md", "# Lokal angepasst\n"}),
	}, http.StatusOK)
	second := c.expect(http.MethodPost, "/api/v1/agents/import?slug=skill-ziel-2", bundle, http.StatusCreated)
	warnJSON, _ := json.Marshal(second["warnings"])
	if !strings.Contains(string(warnJSON), "gemeinsam") {
		t.Fatalf("the note about the existing library skill is missing: %s", warnJSON)
	}
	libAfter := getSkillList(t, c, "/api/v1/skills")
	if len(libAfter) != 1 || libAfter[0].Description != "Örtlich angepasste Fassung" {
		t.Fatalf("an existing library version must not be overwritten: %+v", libAfter)
	}
	if len(libAfter[0].AssignedTo) != 2 {
		t.Fatalf("the second agent must be linked: %+v", libAfter[0])
	}

	// --- A broken skill in the bundle: 400, and no half-created agent. ---
	broken := map[string]any{}
	rawBundle, _ := json.Marshal(bundle)
	json.Unmarshal(rawBundle, &broken)
	broken["skills"] = []map[string]any{{
		"name": "boese", "description": "d", "origin": "agent",
		"files": map[string]string{"SKILL.md": "x", "../../.claude/settings.json": "{}"},
	}}
	c.expect(http.MethodPost, "/api/v1/agents/import?slug=nie-angelegt", broken, http.StatusBadRequest)
	agentList := c.do(http.MethodGet, "/api/v1/agents", nil)
	var all []struct {
		Slug string `json:"slug"`
	}
	json.NewDecoder(agentList.Body).Decode(&all)
	agentList.Body.Close()
	for _, a := range all {
		if a.Slug == "nie-angelegt" {
			t.Fatal("a rejected bundle must not leave an agent behind")
		}
	}

	// --- The config import onto an existing agent pulls the skills along. ---
	tgt := c.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "skill-nachzieher", "display_name": "Nachzieher", "runtime": "mock"}, http.StatusCreated)
	tid := tgt["id"].(string)
	c.expect(http.MethodPost, "/api/v1/agents/"+tid+"/config/import", bundle, http.StatusOK)
	pulled := getSkillList(t, c, "/api/v1/agents/"+tid+"/skills")
	if len(pulled) != 2 {
		t.Fatalf("the config import must bring the skills along: %+v", pulled)
	}
	c.expect(http.MethodPost, "/api/v1/agents/"+tid+"/config/import", broken, http.StatusBadRequest)

	// --- If the config fails at the role boundary, the skills must not have
	// been created either. An error has to mean: nothing happened. ---
	s.mitglied(t, "owner@test.local", "Owner", "agent_owner", "owner-passwort")
	owner := login(t, s, "owner@test.local", "owner-passwort")

	unberührt := c.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "rbac-ziel", "display_name": "RBAC-Ziel", "runtime": "mock"}, http.StatusCreated)
	uid := unberührt["id"].(string)

	// Tool allowlists are changed only by org_admin/security — for the
	// agent_owner the same bundle is a 403.
	mitTools := map[string]any{}
	json.Unmarshal(rawBundle, &mitTools)
	files := mitTools["files"].(map[string]any)
	files["ACCESS.md"] = "- system: zammad   scope: read,write   tools: reply, close"
	owner.expect(http.MethodPost, "/api/v1/agents/"+uid+"/config/import", mitTools, http.StatusForbidden)

	if got := getSkillList(t, c, "/api/v1/agents/"+uid+"/skills"); len(got) != 0 {
		t.Fatalf("an import that failed on RBAC must not leave skills behind: %+v", got)
	}
}

// TestImportConfigOverwrite checks overwriting an EXISTING agent from a bundle
// where ONLY the config files are taken over: the target's config becomes the
// bundle's, its master data (slug, name, model) stays untouched.
func TestImportConfigOverwrite(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	// Source agent with a particular config, then export.
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

	// Target agent with a DIFFERENT config and its own master data.
	tgt := c.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "ziel", "display_name": "Ziel", "runtime": "mock"}, http.StatusCreated)
	tid := tgt["id"].(string)
	c.expect(http.MethodPatch, "/api/v1/agents/"+tid+"/model", map[string]string{"model": "claude-test-9"}, http.StatusOK)
	c.expect(http.MethodPut, "/api/v1/agents/"+tid+"/config", map[string]any{"files": map[string]string{
		"SOUL.md": "# Ziel\n\nAlte Config, wird überschrieben.",
	}}, http.StatusOK)

	// Bundle config import onto the existing target agent.
	c.expect(http.MethodPost, "/api/v1/agents/"+tid+"/config/import", bundle, http.StatusOK)

	// The target's config is now the bundle's ...
	cfg := c.expect(http.MethodGet, "/api/v1/agents/"+tid+"/config", nil, http.StatusOK)
	nfiles := cfg["files"].(map[string]any)
	if nfiles["SOUL.md"] != "# Quelle\n\nGeteilte Basis-Config." {
		t.Fatalf("SOUL.md not overwritten: %q", nfiles["SOUL.md"])
	}
	// ... but the target's master data stays untouched (only the config is taken over).
	after := c.expect(http.MethodGet, "/api/v1/agents/"+tid, nil, http.StatusOK)
	if after["slug"] != "ziel" || after["display_name"] != "Ziel" || after["model"] != "claude-test-9" {
		t.Fatalf("the target's master data was changed: %v", after)
	}

	// The heartbeat from the bundle was materialized (config write-through).
	var count int
	if err := s.pool.QueryRow(t.Context(),
		"SELECT COUNT(*) FROM agent_heartbeats WHERE agent_id=$1 AND name='Ping'", tid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("heartbeat from the bundle not materialized: %d", count)
	}

	// Error cases: unknown agent → 404, broken bundle → 400.
	c.expect(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000000/config/import",
		bundle, http.StatusNotFound)
	c.expect(http.MethodPost, "/api/v1/agents/"+tid+"/config/import",
		map[string]any{"kind": "falsch", "version": 1, "files": map[string]string{"SOUL.md": "x"}}, http.StatusBadRequest)
}
