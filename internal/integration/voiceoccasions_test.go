package integration

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/guardrails"
	"covey/internal/llm"
	"covey/migrations"
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

// The chat half of #471: the person who writes belongs to a department that
// names a chat voice and says how it wants to be spoken to. The triage turn
// reads both, and the answer in the thread says which voice spoke and why.
func TestTheDepartmentReachesTheTriage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("abteilung")
	s.ohneLaeufe(agent.ID)
	modell := &antwortendesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)

	vertrieb := builtVoice(t, admin, "Vertriebston")
	eigen := builtVoice(t, admin, "Eigenton")
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voices", map[string]any{"chat": eigen}, http.StatusOK)
	dept := admin.expect(http.MethodPost, "/api/v1/departments", map[string]any{"name": "Vertrieb"}, http.StatusCreated)["id"].(string)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+dept+"/audience",
		map[string]any{"audience_note": "Erst was es für den Kunden heißt, dann der Rest."}, http.StatusOK)
	admin.expect(http.MethodPut, "/api/v1/departments/"+dept+"/voices", map[string]any{"chat": vertrieb}, http.StatusOK)
	if _, err := s.pool.Exec(ctx, `UPDATE humans SET department_id=$2 WHERE org_id=$1 AND email='admin@test.local'`, s.orgID, dept); err != nil {
		t.Fatal(err)
	}

	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages",
		map[string]any{"text": "Wie steht es um Globex?"}, http.StatusAccepted)
	wartenAuf(t, "the triage answered", func() bool {
		return strings.Contains(strings.Join(eintraege(t, admin, agent.ID), "\n"), "Gern, mach ich.")
	})
	p := triagePrompt(t, modell)
	for _, will := range []string{"How the Vertrieb department wants to be spoken to", "Erst was es für den Kunden heißt",
		`the voice "Vertriebston"`, "Passages in this voice"} {
		if !strings.Contains(p, will) {
			t.Errorf("the triage prompt lacks %q:\n%s", will, p)
		}
	}
	if strings.Contains(p, "Eigenton") {
		t.Error("the department's chat voice has to take the place of the agent's")
	}

	var meta map[string]string
	if err := s.pool.QueryRow(ctx, `SELECT meta FROM conversation_messages
		WHERE author_kind='agent' AND author_id=$1 ORDER BY created_at DESC LIMIT 1`, agent.ID).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if meta["voice"] != "Vertriebston" || meta["voice_reason"] != "department:Vertrieb×chat" || meta["audience"] != "Vertrieb" {
		t.Fatalf("the answer has to say which voice spoke and why: %v", meta)
	}
}

// triagePrompt is the first prompt the model saw for a triage turn — the
// builds before it asked the same model for a card.
func triagePrompt(t *testing.T, m *antwortendesModell) string {
	t.Helper()
	for _, p := range m.prompts() {
		if strings.Contains(p, "The new message:") {
			return p
		}
	}
	t.Fatal("no triage prompt was seen")
	return ""
}

// The run half of #471: the run's TONE.md is the voice of the task's
// occasion — the chat voice for a task from a conversation, with the asking
// person's department line; the customers voice for any other task; the
// publications voice only when the task says so — and the recording names
// the voice and why.
func TestTheRunWritesInTheVoiceOfItsOccasion(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("anlass")
	chatVoice := builtVoice(t, admin, "Chatstimme")
	mailVoice := builtVoice(t, admin, "Kundenstimme")
	blogVoice := builtVoice(t, admin, "Blogstimme")
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voices",
		map[string]any{"chat": chatVoice, "customers": mailVoice}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/voices", map[string]any{"publications": blogVoice}, http.StatusOK)
	dept := admin.expect(http.MethodPost, "/api/v1/departments", map[string]any{"name": "Technik"}, http.StatusCreated)["id"].(string)
	admin.expect(http.MethodPatch, "/api/v1/departments/"+dept+"/audience",
		map[string]any{"audience_note": "Nennt Ticket und Branch."}, http.StatusOK)
	if _, err := s.pool.Exec(ctx, `UPDATE humans SET department_id=$2 WHERE id=$1`, s.adminID, dept); err != nil {
		t.Fatal(err)
	}

	conv := direkt(t, s, s.adminID, agent.ID)
	aus, err := s.backlog.CreateIn(ctx, s.orgID, agent.ID, "Prompt zeigen", "[mock:prompt]", "chat:admin@test.local", 3, &conv)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the conversation task finishes", 40*time.Second, func() bool {
		return s.taskState(aus.ID) == backlog.StateDone
	})
	got, err := s.backlog.Get(ctx, aus.ID)
	if err != nil || got.Result == nil {
		t.Fatalf("result: %v %v", got.Result, err)
	}
	if p := *got.Result; !strings.Contains(p, `This is the voice "Chatstimme"`) || strings.Contains(p, "Kundenstimme") ||
		!strings.Contains(p, "Nennt Ticket und Branch.") {
		t.Fatalf("a conversation task writes in the chat voice, with the department's line:\n%s", p)
	}
	if ev := voiceEvent(t, s, aus.ID); ev["voice"] != "Chatstimme" || ev["voice_reason"] != "agent×chat" || ev["audience"] != "Technik" {
		t.Fatalf("the recording has to name the voice and why: %v", ev)
	}

	mail, p := laufLassen(t, s, agent, "Prompt zeigen", "[mock:prompt]")
	if !strings.Contains(p, `This is the voice "Kundenstimme"`) || strings.Contains(p, "Chatstimme") || strings.Contains(p, "Nennt Ticket") {
		t.Fatalf("any other task writes in the customers voice:\n%s", p)
	}
	if ev := voiceEvent(t, s, mail.ID); ev["voice_reason"] != "agent×customers" || ev["voice_occasion"] != "customers" {
		t.Fatalf("recording: %v", ev)
	}

	blog, p := laufLassen(t, s, agent, "Prompt zeigen", "[mock:prompt]\noccasion: publications")
	if !strings.Contains(p, `This is the voice "Blogstimme"`) {
		t.Fatalf("a task that says it is a publication writes in the publications voice:\n%s", p)
	}
	if ev := voiceEvent(t, s, blog.ID); ev["voice_reason"] != "org×publications" {
		t.Fatalf("recording: %v", ev)
	}
}

// voiceEvent is the recording's note of the voice a run wrote in.
func voiceEvent(t *testing.T, s *stack, taskID uuid.UUID) map[string]any {
	t.Helper()
	var ev map[string]any
	if err := s.pool.QueryRow(context.Background(), `SELECT payload FROM recording_events
		WHERE task_id=$1 AND kind='lifecycle' AND payload ? 'voice_reason' ORDER BY id DESC LIMIT 1`, taskID).Scan(&ev); err != nil {
		t.Fatalf("no voice in the recording: %v", err)
	}
	return ev
}

// 0122 moves the one voice an agent carried into its two outward slots and
// leaves the chat slot empty. Its data step, taken from the migration file
// itself and run once more over an agent with a voice_id — not a migration
// down and up, which would stop testing 0122 as soon as a newer one exists.
func TestTheOneVoiceMovesIntoTheOutwardSlots(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("umzug")
	id := builtVoice(t, admin, "Altstimme")

	raw, err := fs.ReadFile(migrations.FS, "0122_voice_occasions.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	start := strings.Index(sql, "INSERT INTO voice_assignments")
	end := strings.Index(sql[start:], ";")
	if start < 0 || end < 0 {
		t.Fatal("the data step of 0122 is not where the test looks for it")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET voice_id=$2 WHERE id=$1`, agent.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, sql[start:start+end]); err != nil {
		t.Fatal(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT occasion FROM voice_assignments WHERE agent_id=$1 AND voice_id=$2 ORDER BY occasion`, agent.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var occs []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			t.Fatal(err)
		}
		occs = append(occs, o)
	}
	if strings.Join(occs, ",") != "customers,publications" {
		t.Fatalf("the one voice goes into customers and publications, the chat stays empty: %v", occs)
	}
}

// The style gate measures against the voice the task's outward occasion
// resolves to — here the agent's customers slot, with no TONE.md in the
// config at all. Without the slot the gate would record that it had no
// profile.
func TestTheStyleGateReadsTheOutwardVoice(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("aussenstimme")
	id := builtVoice(t, admin, "Aussenstimme")
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voices", map[string]any{"customers": id}, http.StatusOK)
	if _, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: s.orgID, ScopeLevel: "global", RuleType: guardrails.RuleStyleGate,
		Pattern: "covey:*", Enabled: true, Params: json.RawMessage(`{"mode":"warn","min_words":40}`),
	}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"title": "Mit Stimme", "body": styleGenericBody})
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Schreiben",
		"[mock:action covey/create_task "+string(params)+"]\n[mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	var skipped int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='guardrail' AND payload->>'rule'='style_gate' AND payload->>'decision'='skipped'`,
		agent.ID).Scan(&skipped); err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Fatalf("the gate has to find the customers voice's profile, it skipped %d times", skipped)
	}
}

// covey/style_check measures against the voice the task's outward occasion
// resolves to, as the gate would when the text leaves — not against the
// config's TONE.md, which the slot has replaced. Without a slot the config's
// own profile applies, as before #471.
func TestStyleCheckMeasuresAgainstTheOutwardVoice(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("pruefstimme")
	cfg, err := s.registry.CurrentConfig(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	files := cfg.Files
	files["TONE.md"] = styleToneMD
	if _, err := s.registry.SaveConfig(ctx, agent.ID, files, &s.adminID); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"text": styleGenericBody})
	check := func(title string) map[string]any {
		t.Helper()
		task, err := s.backlog.Create(ctx, s.orgID, agent.ID, title,
			"[mock:action covey/style_check "+string(params)+"]\n[mock:result gemessen]", "manual", 3)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "task done", 30*time.Second, func() bool {
			return s.taskState(task.ID) == backlog.StateDone
		})
		var ev map[string]any
		if err := s.pool.QueryRow(ctx, `SELECT payload FROM recording_events
			WHERE task_id=$1 AND kind='action' AND payload->>'action'='covey:style_check' AND payload ? 'profile'`,
			task.ID).Scan(&ev); err != nil {
			t.Fatalf("the check has to record what it measured against: %v", err)
		}
		return ev
	}

	if ev := check("Ohne Stimme"); ev["profile"] != "agent" || ev["voice"] != "" {
		t.Fatalf("without a slot the config's TONE.md applies: %v", ev)
	}

	id := builtVoice(t, admin, "Pruefstimme")
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voices", map[string]any{"customers": id}, http.StatusOK)
	if ev := check("Mit Stimme"); ev["profile"] != "voice" || ev["voice"] != "Pruefstimme" || ev["voice_reason"] != "agent×customers" {
		t.Fatalf("with a customers slot the check measures against that voice: %v", ev)
	}
}
