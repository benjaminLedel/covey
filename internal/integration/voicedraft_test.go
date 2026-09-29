package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/guardrails"
)

// TestAnAgentDraftsAVoiceAPersonReleases: the third source of #458. An agent
// asked in the chat files a voice — from a description or from texts — and
// what arrives is a draft: named after the agent, carried by nobody, with no
// card released and no op to release one.
func TestAnAgentDraftsAVoiceAPersonReleases(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("stimmbildner")

	run := func(params map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(params)
		task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Stimme entwerfen",
			"[mock:action covey/voice_draft "+string(raw)+"]\n[mock:result entworfen]", "manual", 3)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the run is over", 30*time.Second, func() bool {
			st := s.taskState(task.ID)
			return st == backlog.StateDone || st == backlog.StateFailed
		})
	}
	run(map[string]any{"name": "Angebote", "purpose": "offers", "language": "de",
		"description": "Wir siezen, nennen den Preis im ersten Satz und schließen mit einer Frist."})
	texts := []map[string]string{}
	for _, doc := range voiceCorpus {
		texts = append(texts, map[string]string{"name": doc[0], "text": doc[1]})
	}
	run(map[string]any{"name": "Blog aus Texten", "purpose": "blog", "texts": texts})
	// Neither without material, nor over an existing name.
	run(map[string]any{"name": "Leer"})
	run(map[string]any{"name": "angebote", "description": "Noch einmal."})

	list := admin.expectList(http.MethodGet, "/api/v1/voices", nil, http.StatusOK)
	byName := map[string]map[string]any{}
	for _, v := range list {
		byName[v["name"].(string)] = v
	}
	if len(byName) != 2 {
		t.Fatalf("expected two drafts, got %v", list)
	}
	offer := byName["Angebote"]
	if offer["source"] != "described" || offer["purpose"] != "offers" || offer["released_card"] != "" ||
		!strings.Contains(offer["description"].(string), "Preis im ersten Satz") {
		t.Fatalf("described draft: %+v", offer)
	}
	if by, _ := offer["drafted_by"].(map[string]any); by["slug"] != agent.Slug {
		t.Fatalf("the draft names the agent: %+v", offer["drafted_by"])
	}
	if agents, _ := offer["agents"].([]any); len(agents) != 0 {
		t.Fatal("a draft is carried by nobody")
	}
	blog := byName["Blog aus Texten"]
	if blog["source"] != "texts" || blog["version"].(float64) != 0 {
		t.Fatalf("a texts draft waits for a person to build it: %+v", blog)
	}
	detail := admin.expect(http.MethodGet, "/api/v1/voices/"+blog["id"].(string), nil, http.StatusOK)
	if corpus, _ := detail["corpus"].([]any); len(corpus) != len(voiceCorpus) {
		t.Fatalf("the texts are the corpus: %+v", detail["corpus"])
	}
	if detail["assignable"] != false {
		t.Fatal("an unbuilt draft is not assignable")
	}

	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='lifecycle' AND payload->>'status'='voice_draft'`, agent.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("the recording holds %d voice drafts, expected 2", n)
	}

	// A guard rail on the subject governs it like any other covey action.
	if _, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: s.orgID, ScopeLevel: "global", RuleType: guardrails.RuleDenyAction,
		Pattern: "covey:voice_draft", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	run(map[string]any{"name": "Verboten", "description": "Egal."})
	if after := admin.expectList(http.MethodGet, "/api/v1/voices", nil, http.StatusOK); len(after) != 2 {
		t.Fatalf("a denied draft must not be stored: %d voices", len(after))
	}
}
