package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"covey/internal/voice"
)

// A correction pair is what a person made of an agent's text, beside what the
// agent wrote. It is the strongest material a voice can collect — a rule
// describes the hand, a pair shows it (spec/24) — and the approval gate is
// where covey can collect one without anything outside it: both halves are
// already on the screen at the moment somebody decides.
//
// This holds the whole way: the reviewer rewrites, the pair lands on the voice
// the agent carries, and the corrected text reaches the AGENT — which matters
// because the agent performs the action, not the control plane. Without that
// last step the approved text would be the one the reviewer just replaced.
func TestAReviewerRewriteBecomesAPairAndReachesTheAgent(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("writer")

	created := admin.expect(http.MethodPost, "/api/v1/voices",
		map[string]any{"name": "Hausstimme", "language": "de"}, http.StatusCreated)
	voiceID, _ := created["id"].(string)
	for _, doc := range voiceCorpus {
		admin.expect(http.MethodPost, "/api/v1/voices/"+voiceID+"/documents",
			map[string]any{"name": doc[0], "body": doc[1]}, http.StatusCreated)
	}
	admin.expect(http.MethodPost, "/api/v1/voices/"+voiceID+"/build", nil, http.StatusOK)
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voice",
		map[string]any{"voice_id": voiceID}, http.StatusOK)

	// What the agent wants to send, and what a person makes of it.
	entwurf := "Wir möchten Sie darüber in Kenntnis setzen, dass im Rahmen der " +
		"Durchführung der Migration eine Verzögerung eingetreten ist, welche " +
		"voraussichtlich zu einer Verschiebung des Termins führen wird."
	korrigiert := "Die Migration verzögert sich. Der Termin am 14. März fällt, " +
		"einen neuen nennen wir bis Freitag."

	appr, err := s.obs.CreateApproval(ctx, s.orgID, agent.ID, nil, "mail:send",
		map[string]any{"to": "kunde@example.org", "body": entwurf})
	if err != nil {
		t.Fatal(err)
	}
	// The task that waits for this decision — that is the path the corrected
	// text has to travel, so the test has to have one.
	if _, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Kunde informieren", "[mock:result ok]", "manual", 3); err != nil {
		t.Fatal(err)
	}
	// open → in_progress → blocked is the way a task actually gets there; the
	// state machine refuses the shortcut, and rightly so.
	task, err := s.backlog.ClaimNext(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Block(ctx, task.ID, "approval:"+appr.ID.String(), "sess-1", "warte auf Freigabe"); err != nil {
		t.Fatal(err)
	}

	admin.expect(http.MethodPost, "/api/v1/approvals/"+appr.ID.String()+"/decide",
		map[string]any{"approve": true, "text": korrigiert}, http.StatusOK)

	pairs := admin.expectList(http.MethodGet, "/api/v1/voices/"+voiceID+"/corrections", nil, http.StatusOK)
	if len(pairs) != 1 {
		t.Fatalf("expected exactly one pair, got %d", len(pairs))
	}
	p := pairs[0]
	if p["before"] != entwurf || p["after"] != korrigiert {
		t.Fatalf("the pair does not hold both halves: %+v", p)
	}
	if p["source"] != voice.SourceApproval || p["action"] != "mail:send" {
		t.Fatalf("a pair without its origin cannot be weighed: %+v", p)
	}

	// The correction has to travel to the agent: it performs the action, so it
	// has to be told what to perform it with, verbatim. That is what the woken
	// task carries as its resume input.
	var resume string
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(resume_input,'') FROM backlog_tasks WHERE id=$1`, task.ID).Scan(&resume); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resume, korrigiert) {
		t.Fatalf("the agent was not given the corrected text: %q", resume)
	}
	if !strings.Contains(strings.ToLower(resume), "verbatim") {
		t.Errorf("a text the agent may rewrite is not a correction: %q", resume)
	}

	// An approval nobody changed produces no pair — otherwise every click would
	// teach the voice that its own text is the correction.
	appr2, err := s.obs.CreateApproval(ctx, s.orgID, agent.ID, nil, "mail:send",
		map[string]any{"to": "kunde@example.org", "body": entwurf})
	if err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPost, "/api/v1/approvals/"+appr2.ID.String()+"/decide",
		map[string]any{"approve": true, "text": entwurf}, http.StatusOK)
	if again := admin.expectList(http.MethodGet, "/api/v1/voices/"+voiceID+"/corrections", nil, http.StatusOK); len(again) != 1 {
		t.Fatalf("an unchanged text is not a correction: %d pairs", len(again))
	}

	// And a refusal produces none either: what is denied does not go out, so
	// there is nothing to correct.
	appr3, err := s.obs.CreateApproval(ctx, s.orgID, agent.ID, nil, "mail:send",
		map[string]any{"to": "kunde@example.org", "body": entwurf})
	if err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPost, "/api/v1/approvals/"+appr3.ID.String()+"/decide",
		map[string]any{"approve": false, "text": korrigiert}, http.StatusOK)
	if again := admin.expectList(http.MethodGet, "/api/v1/voices/"+voiceID+"/corrections", nil, http.StatusOK); len(again) != 1 {
		t.Fatalf("a denied action leaves nothing to correct: %d pairs", len(again))
	}

	// The pair reaches the build: the card prompt shows the model the
	// transformation rather than describing it.
	full := admin.expect(http.MethodGet, "/api/v1/voices/"+voiceID, nil, http.StatusOK)
	if full["version"] == nil {
		t.Fatal("the voice should be built")
	}
	if !strings.Contains(voice.CardPrompt(builtFromNothing(), "Hausstimme",
		[]voice.Correction{{Before: entwurf, After: korrigiert, Action: "mail:send"}}), korrigiert) {
		t.Error("the card prompt has to carry the person's version")
	}

	// Deleting a pair: a correction made by accident must not teach the voice
	// for good.
	admin.expect(http.MethodDelete,
		"/api/v1/voices/"+voiceID+"/corrections/"+pairs[0]["id"].(string), nil, http.StatusOK)
	if left := admin.expectList(http.MethodGet, "/api/v1/voices/"+voiceID+"/corrections", nil, http.StatusOK); len(left) != 0 {
		t.Fatalf("the pair should be gone: %d", len(left))
	}
}

// builtFromNothing is an empty build — enough to render a prompt around the
// pairs, which is what the assertion above is about.
func builtFromNothing() voice.Built { return voice.Built{} }
