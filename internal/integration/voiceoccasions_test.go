package integration

import (
	"net/http"
	"strings"
	"testing"
)

// builtVoice is a voice measured from the test corpus: assignable without a
// model, because the exemplars of a measured voice act once it is built.
func builtVoice(t *testing.T, admin *apiClient, name string) string {
	t.Helper()
	v := admin.expect(http.MethodPost, "/api/v1/voices", map[string]any{"name": name, "language": "de"}, http.StatusCreated)
	id := v["id"].(string)
	for _, doc := range voiceCorpus {
		admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/documents",
			map[string]any{"name": doc[0], "body": doc[1]}, http.StatusCreated)
	}
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/build", nil, http.StatusOK)
	return id
}

func slot(t *testing.T, m map[string]any, key, occ string) map[string]any {
	t.Helper()
	slots, _ := m[key].(map[string]any)
	got, _ := slots[occ].(map[string]any)
	return got
}

// The slots of #471 through the API: an agent names a voice per occasion, the
// organisation a default, a department its line and its voices; every role
// reads, only the manage roles change; the voice says who uses it for what,
// and the table says who gets what.
func TestVoiceSlotsAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	s.mitglied(t, "auditor@test.local", "Auditor", "auditor", "auditor-passwort")
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")
	agent := s.newSupportAgent("stimmen")
	base := "/api/v1/agents/" + agent.ID.String() + "/voices"

	chatVoice := builtVoice(t, admin, "Kollegial")
	mailVoice := builtVoice(t, admin, "Kundenbrief")
	unbuilt := admin.expect(http.MethodPost, "/api/v1/voices", map[string]any{"name": "Leer"}, http.StatusCreated)["id"].(string)

	// Empty at first, and every occasion is in the answer.
	got := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	for _, occ := range []string{"chat", "customers", "publications"} {
		if slots := got["slots"].(map[string]any); slots[occ] != nil {
			t.Fatalf("a fresh agent names no voice for %s: %v", occ, got)
		}
		if eff := got["effective"].(map[string]any)[occ].(map[string]any); eff["reason"] != "none×"+occ {
			t.Fatalf("effective %s: %v", occ, eff)
		}
	}

	// Refusals: an unknown occasion, an unbuilt voice, a read-only role.
	admin.expect(http.MethodPut, base, map[string]any{"tender": chatVoice}, http.StatusBadRequest)
	admin.expect(http.MethodPut, base, map[string]any{"chat": unbuilt}, http.StatusBadRequest)
	admin.expect(http.MethodPut, base, map[string]any{"chat": "kein-uuid"}, http.StatusBadRequest)
	auditor.expect(http.MethodPut, base, map[string]any{"chat": chatVoice}, http.StatusForbidden)
	auditor.expect(http.MethodGet, base, nil, http.StatusOK)

	set := admin.expect(http.MethodPut, base, map[string]any{"chat": chatVoice, "customers": mailVoice}, http.StatusOK)
	if slot(t, set, "slots", "chat")["voice"] != "Kollegial" || slot(t, set, "slots", "customers")["voice"] != "Kundenbrief" {
		t.Fatalf("slots: %v", set)
	}
	if set["slots"].(map[string]any)["publications"] != nil {
		t.Fatalf("an occasion not named is empty: %v", set)
	}
	if slot(t, set, "effective", "customers")["reason"] != "agent×customers" {
		t.Fatalf("effective: %v", set)
	}

	// The organisation's default fills what the agent leaves empty; PATCH
	// leaves the occasions it does not name.
	auditor.expect(http.MethodPatch, "/api/v1/org/voices", map[string]any{"publications": mailVoice}, http.StatusForbidden)
	admin.expect(http.MethodPatch, "/api/v1/org/voices", map[string]any{"publications": mailVoice, "chat": chatVoice}, http.StatusOK)
	orgSlots := admin.expect(http.MethodPatch, "/api/v1/org/voices", map[string]any{"chat": ""}, http.StatusOK)
	if slot(t, orgSlots, "slots", "publications")["voice"] != "Kundenbrief" || orgSlots["slots"].(map[string]any)["chat"] != nil {
		t.Fatalf("org slots: %v", orgSlots)
	}
	if eff := slot(t, admin.expect(http.MethodGet, base, nil, http.StatusOK), "effective", "publications"); eff["reason"] != "org×publications" || eff["voice"] != "Kundenbrief" {
		t.Fatalf("the org default has to fill the empty slot: %v", eff)
	}

	// A department: its line (bounded) and its voices.
	dept := admin.expect(http.MethodPost, "/api/v1/departments", map[string]any{"name": "Vertrieb"}, http.StatusCreated)
	deptID := dept["id"].(string)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+deptID+"/audience",
		map[string]any{"audience_note": strings.Repeat("x", 401)}, http.StatusBadRequest)
	auditor.expect(http.MethodPatch, "/api/v1/departments/"+deptID+"/audience",
		map[string]any{"audience_note": "Sagt uns, was es für den Kunden heißt."}, http.StatusForbidden)
	note := admin.expect(http.MethodPatch, "/api/v1/departments/"+deptID+"/audience",
		map[string]any{"audience_note": "  Sagt uns, was es\nfür den Kunden heißt. "}, http.StatusOK)
	if note["audience_note"] != "Sagt uns, was es für den Kunden heißt." {
		t.Fatalf("note: %v", note)
	}
	dv := admin.expect(http.MethodPut, "/api/v1/departments/"+deptID+"/voices", map[string]any{"chat": mailVoice}, http.StatusOK)
	if slot(t, dv, "voices", "chat")["voice"] != "Kundenbrief" {
		t.Fatalf("department voices: %v", dv)
	}
	auditor.expect(http.MethodPut, "/api/v1/departments/"+deptID+"/voices", map[string]any{"chat": mailVoice}, http.StatusForbidden)
	var listed map[string]any
	for _, d := range admin.expectList(http.MethodGet, "/api/v1/departments", nil, http.StatusOK) {
		if d["id"] == deptID {
			listed = d
		}
	}
	if listed["audience_note"] != "Sagt uns, was es für den Kunden heißt." || listed["voices"].(map[string]any)["chat"] != mailVoice {
		t.Fatalf("the department list carries the line and the voices: %v", listed)
	}

	// Who uses the voice for what.
	detail := admin.expect(http.MethodGet, "/api/v1/voices/"+mailVoice, nil, http.StatusOK)
	uses, _ := detail["used_by"].([]any)
	var seen []string
	for _, u := range uses {
		m := u.(map[string]any)
		seen = append(seen, m["holder"].(string)+":"+m["name"].(string)+":"+m["occasion"].(string))
	}
	want := []string{"agent:" + agent.DisplayName + ":customers", "department:Vertrieb:chat", "org::publications"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("used_by %v, want %v", seen, want)
	}

	// Who gets what: Vertrieb × chat is the department's; nobody's department
	// × chat with the agent is the agent's; without the agent there is none.
	table := admin.expect(http.MethodGet, "/api/v1/voices/assignments?agent_id="+agent.ID.String(), nil, http.StatusOK)
	cells := map[string]map[string]any{}
	for _, r := range table["rows"].([]any) {
		row := r.(map[string]any)
		name := "-"
		if d, ok := row["department"].(map[string]any); ok {
			name = d["name"].(string)
		}
		for occ, c := range row["cells"].(map[string]any) {
			cells[name+"×"+occ] = c.(map[string]any)
		}
	}
	for key, reason := range map[string]string{
		"Vertrieb×chat":      "department:Vertrieb×chat",
		"Vertrieb×customers": "agent×customers",
		"-×chat":             "agent×chat",
		"-×publications":     "org×publications",
	} {
		if cells[key]["reason"] != reason {
			t.Errorf("%s: %v, want %s", key, cells[key], reason)
		}
	}
	plain := auditor.expect(http.MethodGet, "/api/v1/voices/assignments", nil, http.StatusOK)
	for _, r := range plain["rows"].([]any) {
		row := r.(map[string]any)
		if row["department"] == nil {
			if c := row["cells"].(map[string]any)["chat"].(map[string]any); c["reason"] != "none×chat" {
				t.Errorf("without an agent, level 2 does not count: %v", c)
			}
		}
	}
	admin.expect(http.MethodGet, "/api/v1/voices/assignments?agent_id=nope", nil, http.StatusBadRequest)

	// The one-voice assignment from before #471 fills both outward slots.
	other := s.newSupportAgent("einstimmig")
	admin.expect(http.MethodPut, "/api/v1/agents/"+other.ID.String()+"/voice", map[string]any{"voice_id": mailVoice}, http.StatusOK)
	o := admin.expect(http.MethodGet, "/api/v1/agents/"+other.ID.String()+"/voices", nil, http.StatusOK)
	if slot(t, o, "slots", "customers")["voice"] != "Kundenbrief" || slot(t, o, "slots", "publications")["voice"] != "Kundenbrief" ||
		o["slots"].(map[string]any)["chat"] != nil {
		t.Fatalf("the one voice goes into customers and publications: %v", o)
	}
}
