package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Config as code: an agent's configuration leaves as a bundle and comes back
// as one. The round trip is the claim — an export nothing can import is a
// backup that is not one.
func TestAgentBundleRoundTripOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("bündel-agent")

	resp := admin.do(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/export", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: HTTP %d", resp.StatusCode)
	}
	// The export is offered as a file — the browser is supposed to save it,
	// not render it.
	if cd := resp.Header.Get("Content-Disposition"); cd == "" {
		t.Error("the export carries no file name")
	}
	var bundle map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&bundle); err != nil {
		t.Fatal(err)
	}
	if bundle["exported_at"] == nil {
		t.Error("the bundle does not say when it was taken")
	}
	inner, _ := bundle["agent"].(map[string]any)
	if inner == nil {
		t.Fatalf("the bundle carries no agent: %v", bundle)
	}

	// The same bundle under a new slug is a second agent — that is how a
	// configuration that works is passed on.
	inner["slug"] = "bündel-kopie"
	made := admin.expect(http.MethodPost, "/api/v1/agents/import", bundle, http.StatusCreated)
	copied, _ := made["agent"].(map[string]any)
	if copied == nil || copied["slug"] != "bündel-kopie" {
		t.Fatalf("the import produced %v", made)
	}

	// And onto an EXISTING agent it replaces only the configuration. The
	// master data, the board and the guard rails stay where they are.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/config/import",
		bundle, http.StatusOK)

	admin.expect(http.MethodPost, "/api/v1/agents/import", map[string]any{}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/agents/"+uuid.NewString()+"/config/import",
		bundle, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/agents/keine-uuid/export", nil, http.StatusBadRequest)
}

// The setup asks three things and every one of them can be skipped. What it
// must not do is accept an engine this binary does not carry, or a credential
// of a kind that engine does not declare — both would be stored and fail at
// the first wake, in a message pointing at the token.
func TestSetupOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	state := admin.expect(http.MethodGet, "/api/v1/setup/state", nil, http.StatusOK)
	if state == nil {
		t.Fatal("the setup has no state")
	}

	// The credential is checked against the provider before it is stored —
	// "saved" and "works" must not be two different things. Which of the two
	// answers comes back therefore depends on whether this machine has a way
	// out: checked and invalid is a 400, unable to check is a 200 and the
	// value is stored. Both are correct, and the invariant across them is the
	// one worth holding: the answer always says WHETHER it was checked, and a
	// credential the provider rejected is never stored.
	resp := admin.do(http.MethodPost, "/api/v1/setup/engine",
		map[string]string{"engine": "claude-code", "kind": "api_key", "value": "sk-ant-erfunden"})
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	check, _ := body["check"].(map[string]any)
	if check == nil {
		t.Fatalf("the answer carries no check result: %v", body)
	}
	checked, _ := check["checked"].(bool)
	valid, _ := check["valid"].(bool)
	switch {
	case checked && !valid:
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("a credential the provider rejected answered HTTP %d", resp.StatusCode)
		}
		if body["error"] == nil {
			t.Error("the refusal says nothing about what is wrong")
		}
	case !checked:
		if resp.StatusCode != http.StatusOK {
			t.Errorf("an unchecked credential answered HTTP %d", resp.StatusCode)
		}
	default:
		t.Errorf("an invented key was reported as valid: %v", check)
	}

	admin.expect(http.MethodPost, "/api/v1/setup/engine",
		map[string]string{"engine": "gibtesnicht", "kind": "api_key", "value": "x"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/setup/engine",
		map[string]string{"engine": "claude-code", "kind": "zauberwort", "value": "x"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/setup/engine",
		map[string]string{"engine": "claude-code", "kind": "api_key"}, http.StatusBadRequest)

	// What the company does goes into the agents' prompts, so it is asked here
	// rather than left to each agent's SOUL.md.
	admin.expect(http.MethodPost, "/api/v1/setup/org",
		map[string]string{"name": "Beispiel GmbH", "description": "Wir bauen Brücken."}, http.StatusOK)

	org := admin.expect(http.MethodGet, "/api/v1/org", nil, http.StatusOK)
	if org["description"] != "Wir bauen Brücken." {
		t.Errorf("the description did not arrive: %v", org["description"])
	}
}

// Users and organisations: who may be in, and who may create one. The last
// admin cannot be removed — an organisation nobody administers is one nobody
// gets back into.
func TestUserAdministrationOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	made := admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "neu@test.local", "display_name": "Neu", "role": "agent_owner",
		"password": "neu-passwort",
	}, http.StatusCreated)
	id, _ := made["id"].(string)
	if id == "" {
		t.Fatalf("the user carries no id: %v", made)
	}

	// The address exists once — two seats under one login would be two people
	// the org chart cannot tell apart.
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "neu@test.local", "display_name": "Nochmal", "role": "agent_owner",
		"password": "neu-passwort",
	}, http.StatusConflict)
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "ohne-rolle@test.local", "display_name": "X", "role": "kaiserin",
		"password": "x-passwort",
	}, http.StatusBadRequest)

	list := admin.expectList(http.MethodGet, "/api/v1/users", nil, http.StatusOK)
	if len(list) < 2 {
		t.Fatalf("the list holds %d seats", len(list))
	}

	admin.expect(http.MethodPatch, "/api/v1/users/"+id, map[string]string{"role": "auditor"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/users/"+id, map[string]string{"role": "kaiserin"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/users/"+uuid.NewString(),
		map[string]string{"role": "auditor"}, http.StatusNotFound)

	admin.expect(http.MethodDelete, "/api/v1/users/"+id, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/users/keine-uuid", nil, http.StatusBadRequest)

	// The last administrator stays. The way back would be shell access.
	admin.expect(http.MethodDelete, "/api/v1/users/"+s.adminID.String(), nil, http.StatusConflict)
}

// Target systems are plugins, and which of them an organisation has switched
// on is its own decision. The endpoints around that are what the store page
// drives.
func TestTargetSystemsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("ziel-agent")

	list := admin.expectList(http.MethodGet, "/api/v1/targets", nil, http.StatusOK)
	if len(list) == 0 {
		t.Fatal("no target system is offered")
	}
	var name string
	for _, tgt := range list {
		if n, _ := tgt["name"].(string); n != "" {
			name = n
			break
		}
	}

	admin.expect(http.MethodGet, "/api/v1/targets/"+name+"/setup", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/targets/gibtesnicht/setup", nil, http.StatusNotFound)

	admin.expectList(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/systems", nil, http.StatusOK)
	// A system the agent does not carry has no tool allowlist, and an empty
	// list is the honest answer — not a 404, which would say the ENDPOINT is
	// wrong rather than that nothing is allowed there.
	admin.expectList(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/tools/gibtesnicht", nil, http.StatusOK)

	// An MCP plugin names its own endpoint, and installing it opens the
	// organisation's egress to that host — so a body that names none is
	// refused rather than stored empty.
	admin.expect(http.MethodPost, "/api/v1/targets/mcp", map[string]any{}, http.StatusBadRequest)
	admin.expect(http.MethodDelete, "/api/v1/targets/gibtesnicht", nil, http.StatusNotFound)
}

// A bundle carries more than the config files: the board's columns, the guard
// rails, the egress templates, the skills and the NAMES of the secrets. What
// the import refuses is the interesting half — a bundle that does not fit must
// not produce half an agent.
func TestImportRefusesWhatDoesNotFit(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("ausfuhr-agent")

	base := admin.expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/export", nil, http.StatusOK)

	// Kind and version are the two things that say what this file IS. A
	// mismatch is refused rather than read optimistically — half an agent from
	// a bundle nobody can name is worse than no agent.
	wrongKind := clone(base)
	wrongKind["kind"] = "etwas-anderes"
	admin.expect(http.MethodPost, "/api/v1/agents/import", wrongKind, http.StatusBadRequest)

	wrongVersion := clone(base)
	wrongVersion["version"] = 999
	admin.expect(http.MethodPost, "/api/v1/agents/import", wrongVersion, http.StatusBadRequest)

	noSlug := clone(base)
	noSlug["agent"] = map[string]any{"display_name": "Ohne Slug"}
	admin.expect(http.MethodPost, "/api/v1/agents/import", noSlug, http.StatusBadRequest)

	// The slug the bundle names already exists here — the query parameter is
	// how the same configuration is brought in a second time.
	admin.expect(http.MethodPost, "/api/v1/agents/import", base, http.StatusConflict)
	admin.expect(http.MethodPost, "/api/v1/agents/import?slug=ausfuhr-kopie", base, http.StatusCreated)

	// TOOLS.md was merged into ACCESS.md. A bundle still carrying it is named
	// rather than silently losing its tool allowlist.
	withTools := clone(base)
	files := map[string]any{"SOUL.md": "# X\n", "TOOLS.md": "- zammad: reply\n"}
	withTools["files"] = files
	withTools["agent"] = map[string]any{"slug": "mit-tools", "display_name": "Mit Tools"}
	admin.expect(http.MethodPost, "/api/v1/agents/import", withTools, http.StatusBadRequest)

	// An unreadable heartbeat is refused at the import, where somebody is
	// looking — not at the first interval, where it would wake nothing and say
	// nothing.
	badBeat := clone(base)
	badBeat["files"] = map[string]any{"SOUL.md": "# X\n", "HEARTBEAT.md": "alle: manchmal\n"}
	badBeat["agent"] = map[string]any{"slug": "kaputter-takt", "display_name": "Kaputter Takt"}
	admin.expect(http.MethodPost, "/api/v1/agents/import", badBeat, http.StatusBadRequest)

	// An effort level the engine does not know, and a model it cannot route,
	// are refused for the same reason: both would be stored and fail at the
	// first wake.
	badEffort := clone(base)
	badEffort["agent"] = map[string]any{"slug": "falscher-aufwand", "display_name": "X",
		"runtime": "mock", "effort": "gemächlich"}
	admin.expect(http.MethodPost, "/api/v1/agents/import", badEffort, http.StatusBadRequest)

	admin.expect(http.MethodPost, "/api/v1/agents/import", "kein bündel", http.StatusBadRequest)
}

// clone makes a shallow copy so a test can bend one field without disturbing
// the bundle the others read.
func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// The setup's third card hires the People department — the agent that writes
// the configuration for other agents. It is idempotent, because the setup can
// be walked through twice, and a second People department would be two agents
// competing to write the same configs.
func TestSetupPeopleIsIdempotent(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	first := admin.expect(http.MethodPost, "/api/v1/setup/people",
		map[string]any{"display_name": "Petra Personal", "slug": "people", "onboard": false}, http.StatusCreated)
	made, _ := first["agent"].(map[string]any)
	if made == nil {
		t.Fatalf("no agent was hired: %v", first)
	}
	if existed, _ := first["existed"].(bool); existed {
		t.Error("the first call reported an agent that was already there")
	}

	second := admin.expect(http.MethodPost, "/api/v1/setup/people",
		map[string]any{"display_name": "Petra Personal", "slug": "people"}, http.StatusOK)
	if existed, _ := second["existed"].(bool); !existed {
		t.Errorf("a second People department was hired: %v", second)
	}

	// And the setup state now says so, which is what stops the card being
	// offered again.
	state := admin.expect(http.MethodGet, "/api/v1/setup/state", nil, http.StatusOK)
	if state == nil {
		t.Fatal("the setup has no state")
	}
}

// The config assistant needs a model credential. Without one it says so with a
// precondition rather than failing at the first request — the interface uses
// exactly that to decide whether to offer the assistant at all.
func TestConfigAssistantWithoutACredential(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("berater-agent")

	status := admin.expect(http.MethodGet, "/api/v1/assist/status", nil, http.StatusOK)
	if available, _ := status["available"].(bool); available {
		t.Error("the assistant reports itself available without a credential")
	}

	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/config/assist",
		map[string]any{"messages": []any{}}, http.StatusPreconditionFailed)
	admin.expect(http.MethodPost, "/api/v1/agents/keine-uuid/config/assist",
		map[string]any{}, http.StatusBadRequest)
}

// A bundle carries more than the config files, and the point of the import is
// that all of it arrives: the board's columns, the guard rails, the egress
// templates with their hosts, the skills in full — and the NAMES of the
// secrets, never their values, because a bundle travels and a secret must not.
func TestAFullBundleArrivesCompletely(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	bundle := map[string]any{
		"kind":    "covey.agent-config",
		"version": 1,
		"agent": map[string]any{
			"slug": "voll-agent", "display_name": "Voll Ausgestattet",
			"runtime": "mock", "max_turns": 25,
		},
		"files": map[string]any{
			"SOUL.md":   "# Voll\n\n## Role\nAlles dabei.",
			"ACCESS.md": "- system: zammad scope: read,write",
		},
		"stages": []any{
			map[string]any{"name": "In Prüfung", "color": "#cc7a5b"},
			map[string]any{"name": "Wartet", "color": "#6b675e"},
		},
		"guardrails": []any{
			map[string]any{"rule_type": "deny_action", "pattern": "zammad:delete_ticket", "enabled": true},
		},
		"egress_templates": []any{
			map[string]any{"name": "Zammad", "description": "das Ticketsystem", "hosts": []any{
				map[string]any{"pattern": "zammad.example.test", "note": "API"},
			}},
		},
		"skills": []any{
			map[string]any{"name": "rueckfrage", "description": "wie man nachfragt", "origin": "agent",
				"files": map[string]any{"SKILL.md": "---\nname: rueckfrage\ndescription: wie man nachfragt\n---\n\nFrag nach."}},
		},
		"secrets": map[string]any{"org_keys": []string{"zammad_token"}},
	}

	made := admin.expect(http.MethodPost, "/api/v1/agents/import", bundle, http.StatusCreated)
	agent, _ := made["agent"].(map[string]any)
	if agent == nil {
		t.Fatalf("no agent came out: %v", made)
	}
	id, _ := agent["id"].(string)
	if id == "" {
		t.Fatalf("the imported agent has no id: %v", agent)
	}

	// A bundle naming a secret the instance does not have cannot assign it, and
	// says so rather than producing an agent that fails at its first run for a
	// reason nobody wrote down.
	warnings, _ := made["warnings"].([]any)
	if len(warnings) == 0 {
		t.Error("the import reported nothing at all — the draft alone is worth a sentence")
	}

	stages := admin.expectList(http.MethodGet, "/api/v1/agents/"+id+"/stages", nil, http.StatusOK)
	if len(stages) < 2 {
		t.Errorf("the board's columns did not arrive: %v", stages)
	}
	rails := admin.expectList(http.MethodGet, "/api/v1/guardrails", nil, http.StatusOK)
	if len(rails) == 0 {
		t.Error("the guard rail did not arrive")
	}
	templates := admin.expectList(http.MethodGet, "/api/v1/egress/templates", nil, http.StatusOK)
	if len(templates) == 0 {
		t.Error("the egress template did not arrive")
	}
	skills := admin.expectList(http.MethodGet, "/api/v1/agents/"+id+"/skills", nil, http.StatusOK)
	if len(skills) == 0 {
		t.Error("the skill did not arrive")
	}

	// And what comes back out is the same bundle again: that is what makes it
	// a way to pass a configuration on rather than a one-way export.
	back := admin.expect(http.MethodGet, "/api/v1/agents/"+id+"/export", nil, http.StatusOK)
	for _, part := range []string{"stages", "guardrails", "skills"} {
		if back[part] == nil {
			t.Errorf("the export dropped %q", part)
		}
	}
	// The egress templates are the exception, and deliberately so: the export
	// carries the ones ASSIGNED to this agent, not every template the
	// organisation has. An import creates the template but assigns nothing —
	// what an agent may reach is a decision somebody makes here, not one a
	// bundle brings along.
	tid, _ := templates[0]["id"].(string)
	admin.expect(http.MethodPut, "/api/v1/agents/"+id+"/egress/templates/"+tid, nil, http.StatusOK)
	withEgress := admin.expect(http.MethodGet, "/api/v1/agents/"+id+"/export", nil, http.StatusOK)
	if withEgress["egress_templates"] == nil {
		t.Error("an assigned egress template is still missing from the export")
	}
	// Never the values — only the names.
	if secrets, ok := back["secrets"].(map[string]any); ok {
		if _, leaked := secrets["values"]; leaked {
			t.Error("the export carries secret values")
		}
	}
}
