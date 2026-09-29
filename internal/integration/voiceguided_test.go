package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/guardrails"
	"covey/internal/llm"
)

// voiceModel answers the three calls that write for a voice (#458) by what
// they ask: a description, a revision, a sample. It records what it was asked,
// so the tests can see which tier and which texts went in.
type voiceModel struct {
	mu    sync.Mutex
	calls []llm.Request
	round int
}

func (m *voiceModel) Name() string { return "test" }

func (m *voiceModel) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, req)
	prompt := req.Messages[0].Content
	switch {
	case strings.Contains(prompt, "## The description"):
		return "```json\n" + `{"card":"Eröffnung: mit dem Anliegen des Kunden.\n\nNie: Floskeln.",
"exemplars":[
 {"role":"opening","text":"Guten Tag Frau Weber, Ihre Lieferung vom 14. März ist unterwegs."},
 {"role":"evidence","text":"Die Sendungsnummer lautet 4711, zugestellt wird am Donnerstag."},
 {"role":"example","text":"Ein Beispiel: Wer bis 12 Uhr bestellt, bekommt die Ware am Folgetag."},
 {"role":"short","text":"Das ist erledigt."},
 {"role":"closing","text":"Bei Fragen erreichen Sie mich unter 030 1234."}],
"chat_tone":{"address":"sie","tone":"matter_of_fact","emoji":"never","note":""}}` + "\n```", nil
	case strings.Contains(prompt, "Revise the description"):
		m.round++
		card := "Eröffnung: sofort mit der Zahl.\n\nNie: Floskeln."
		if m.round > 1 {
			card = "Eröffnung: mit Datum und Zahl.\n\nNie: Floskeln."
		}
		return `{"card":"` + strings.ReplaceAll(card, "\n", `\n`) + `","exemplars":[
 {"role":"opening","text":"Ihre Lieferung 4711 kommt am Donnerstag, dem 16. März."},
 {"role":"closing","text":"Fragen? 030 1234."}]}`, nil
	}
	return "Guten Tag, Ihre Bestellung 4711 ist unterwegs und kommt am Donnerstag.", nil
}

func (m *voiceModel) requests() []llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]llm.Request(nil), m.calls...)
}

// TestADescribedVoiceIsWrittenHeardRefinedAndReleased walks the second source
// of #458: a description in plain words becomes card, exemplars and a
// suggested chat tone; a person hears it, refines it by instruction, releases
// it — and only then can an agent carry it, as a TONE.md without a profile.
func TestADescribedVoiceIsWrittenHeardRefinedAndReleased(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	model := &voiceModel{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return model, nil }
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("kundendienst")

	admin.expect(http.MethodPost, "/api/v1/voices",
		map[string]any{"name": "Falsch", "purpose": "poetry"}, http.StatusBadRequest)
	created := admin.expect(http.MethodPost, "/api/v1/voices",
		map[string]any{"name": "Kundendienst", "language": "de", "purpose": "support_mail"}, http.StatusCreated)
	id := created["id"].(string)
	if created["purpose"] != "support_mail" || created["source"] != "texts" {
		t.Fatalf("created: %+v", created)
	}

	described := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/describe",
		map[string]any{"description": "Wir siezen, schreiben kurz, nennen immer die Sendungsnummer."}, http.StatusOK)
	if described["source"] != "described" || !strings.HasPrefix(described["card"].(string), "Eröffnung") {
		t.Fatalf("described: %+v", described)
	}
	if ex, _ := described["draft_exemplars"].([]any); len(ex) != 5 {
		t.Fatalf("the exemplars wait as a draft: %+v", described["draft_exemplars"])
	}
	if ex, _ := described["exemplars"].([]any); len(ex) != 0 || described["released_card"] != "" {
		t.Fatalf("nothing of a description acts before the release: %+v", described)
	}
	if tone, _ := described["suggested_chat_tone"].(map[string]any); tone["address"] != "sie" {
		t.Fatalf("suggested tone: %+v", described["suggested_chat_tone"])
	}
	if tone, _ := described["chat_tone"].(map[string]any); len(tone) != 0 {
		t.Fatalf("a suggestion is not the tone — the tone acts once set: %+v", tone)
	}
	if req := model.requests()[0]; req.MaxTokens == 0 {
		t.Error("the description call has to be bounded")
	}

	// Not yet carried: everything in it was written by a model.
	detail := admin.expect(http.MethodGet, "/api/v1/voices/"+id, nil, http.StatusOK)
	if detail["assignable"] != false {
		t.Fatalf("an unreleased described voice is not assignable: %+v", detail["assignable"])
	}
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voice",
		map[string]any{"voice_id": id}, http.StatusBadRequest)

	// Heard: one sample, on the fast tier, stored nowhere.
	sample := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/preview",
		map[string]any{"topic": "Lieferverzug"}, http.StatusOK)
	if !strings.Contains(sample["text"].(string), "4711") || sample["kind"] != "mail" {
		t.Fatalf("preview: %+v", sample)
	}
	last := model.requests()[len(model.requests())-1]
	if last.Tier != llm.TierFast || last.MaxTokens == 0 {
		t.Errorf("preview: tier %s, max tokens %d", last.Tier, last.MaxTokens)
	}
	if !strings.Contains(last.Messages[0].Content, "Sendungsnummer lautet 4711") {
		t.Error("the preview has to hear the draft exemplars")
	}
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/preview", map[string]any{"topic": ""}, http.StatusBadRequest)

	// Refined: a draft, with the text before beside it.
	refined := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/refine",
		map[string]any{"instruction": "konkreter, mehr Zahlen"}, http.StatusOK)
	before, _ := refined["before"].(map[string]any)
	after, _ := refined["voice"].(map[string]any)
	if !strings.HasPrefix(before["card"].(string), "Eröffnung: mit dem Anliegen") ||
		!strings.HasPrefix(after["card"].(string), "Eröffnung: sofort mit der Zahl") {
		t.Fatalf("before/after: %+v / %+v", before["card"], after["card"])
	}
	if after["released_card"] != "" {
		t.Fatal("a refinement is a draft")
	}

	// Released: the card and the model's exemplars act together.
	released := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/release", map[string]any{}, http.StatusOK)
	if ex, _ := released["exemplars"].([]any); len(ex) != 2 {
		t.Fatalf("the draft exemplars are released with the card: %+v", released["exemplars"])
	}
	if ex, _ := released["draft_exemplars"].([]any); len(ex) != 0 {
		t.Fatalf("nothing is pending after a release: %+v", released["draft_exemplars"])
	}

	// A second refinement does not touch what was released.
	again := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/refine",
		map[string]any{"instruction": "mit Datum"}, http.StatusOK)
	v2 := again["voice"].(map[string]any)
	if !strings.HasPrefix(v2["released_card"].(string), "Eröffnung: sofort mit der Zahl") ||
		!strings.HasPrefix(v2["card"].(string), "Eröffnung: mit Datum") {
		t.Fatalf("the released card stays until the next release: %+v", v2)
	}
	// The released version can still be heard beside the draft.
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/preview",
		map[string]any{"topic": "Lieferverzug", "version": "released", "kind": "chat"}, http.StatusOK)
	last = model.requests()[len(model.requests())-1]
	if !strings.Contains(last.Messages[0].Content, "sofort mit der Zahl") || strings.Contains(last.Messages[0].Content, "mit Datum und Zahl") {
		t.Errorf("version released has to hear the released card:\n%s", last.Messages[0].Content)
	}

	// Carried: a TONE.md that says it was described, and no profile for the
	// gate to find.
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voice",
		map[string]any{"voice_id": id}, http.StatusOK)
	cfg, err := s.registry.CurrentConfig(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	tone := cfg.Files["TONE.md"]
	if !strings.Contains(tone, "described in words") || strings.Contains(tone, "```style-profile") {
		t.Fatalf("TONE.md of a described voice:\n%s", tone)
	}

	// Every role reads; only the manage roles build, and the calls that cost
	// are among them.
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "ada@test.local", "display_name": "Ada", "role": "auditor", "password": "ada-passwort",
	}, http.StatusCreated)
	ada := login(t, s, "ada@test.local", "ada-passwort")
	ada.expect(http.MethodGet, "/api/v1/voices/"+id, nil, http.StatusOK)
	calls := len(model.requests())
	for _, path := range []string{"/describe", "/preview", "/refine"} {
		ada.expect(http.MethodPost, "/api/v1/voices/"+id+path,
			map[string]any{"description": "x", "topic": "x", "instruction": "x"}, http.StatusForbidden)
	}
	ada.expect(http.MethodPatch, "/api/v1/voices/"+id, map[string]any{"purpose": "blog"}, http.StatusForbidden)
	if len(model.requests()) != calls {
		t.Error("a refused call must not reach the model")
	}
	admin.expect(http.MethodPatch, "/api/v1/voices/"+id, map[string]any{"purpose": "chat"}, http.StatusOK)
}

// TestTextsTurnADescribedVoiceIntoAMeasuredOne: the gate has nothing to check
// against a described voice — until texts are uploaded and it is built. Then
// it is measured like any other, and the card somebody released stays.
func TestTextsTurnADescribedVoiceIntoAMeasuredOne(t *testing.T) {
	s := newStack(t)
	model := &voiceModel{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return model, nil }
	admin := login(t, s, "admin@test.local", "admin-passwort")

	id := admin.expect(http.MethodPost, "/api/v1/voices",
		map[string]any{"name": "Blog", "language": "de", "purpose": "blog"}, http.StatusCreated)["id"].(string)
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/describe",
		map[string]any{"description": "Kurze Sätze, immer eine Zahl."}, http.StatusOK)
	released := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/release", map[string]any{"card": "Freigegeben."}, http.StatusOK)
	if released["source"] != "described" {
		t.Fatalf("%+v", released)
	}

	// The checks say what is missing while the texts go in.
	detail := admin.expect(http.MethodGet, "/api/v1/voices/"+id, nil, http.StatusOK)
	checks, _ := detail["checks"].([]any)
	if len(checks) != 1 || checks[0].(map[string]any)["code"] != "no_texts" {
		t.Fatalf("checks without texts: %+v", checks)
	}
	for _, doc := range voiceCorpus {
		admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/documents",
			map[string]any{"name": doc[0], "body": doc[1]}, http.StatusCreated)
	}
	detail = admin.expect(http.MethodGet, "/api/v1/voices/"+id, nil, http.StatusOK)
	var codes []string
	for _, c := range detail["checks"].([]any) {
		codes = append(codes, c.(map[string]any)["code"].(string))
	}
	// The test corpus is four short texts: none carries a band.
	if !strings.Contains(strings.Join(codes, ","), "more_texts") || !strings.Contains(strings.Join(codes, ","), "short_texts") {
		t.Fatalf("checks: %v", codes)
	}

	built := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/build", nil, http.StatusOK)
	if built["source"] != "texts" {
		t.Fatalf("a build makes a voice measured: %+v", built["source"])
	}
	if p, _ := built["profile"].(map[string]any); len(p["bands"].(map[string]any)) == 0 {
		t.Fatal("a measured voice has bands")
	}
	if built["released_card"] != "Freigegeben." {
		t.Fatalf("a build does not overwrite a released card: %+v", built["released_card"])
	}
	// Measured now: its exemplars are quotes, and it is not described over.
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/describe",
		map[string]any{"description": "Anders."}, http.StatusConflict)
}

// TestTheStyleGateSkipsADescribedVoice: an agent carrying a described voice
// has a TONE.md with no profile. The gate records why it did not apply and
// lets the action pass — the decision in #458.
func TestTheStyleGateSkipsADescribedVoice(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	model := &voiceModel{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return model, nil }
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("beschrieben")

	id := admin.expect(http.MethodPost, "/api/v1/voices",
		map[string]any{"name": "Beschrieben", "language": "de"}, http.StatusCreated)["id"].(string)
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/describe",
		map[string]any{"description": "Kurz und konkret."}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/release", map[string]any{}, http.StatusOK)
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voice",
		map[string]any{"voice_id": id}, http.StatusOK)
	if _, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: s.orgID, ScopeLevel: "global", RuleType: guardrails.RuleStyleGate,
		Pattern: "covey:create_task", Enabled: true, Params: json.RawMessage(`{"mode":"deny","min_words":40}`),
	}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"title": "Beschrieben geschrieben", "body": styleGenericBody})
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Schreiben",
		"[mock:action covey/create_task "+string(params)+"]\n[mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run is over", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	var reason string
	if err := s.pool.QueryRow(ctx, `SELECT payload->>'reason' FROM recording_events
		WHERE agent_id=$1 AND kind='guardrail' AND payload->>'rule'='style_gate' AND payload->>'decision'='skipped'`,
		agent.ID).Scan(&reason); err != nil {
		t.Fatalf("the gate has to record that it skipped: %v", err)
	}
	if !strings.Contains(reason, "described, not measured") {
		t.Errorf("reason: %q", reason)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM backlog_tasks WHERE org_id=$1 AND title='Beschrieben geschrieben'`,
		s.orgID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the action has to pass: %d tasks", n)
	}
}
