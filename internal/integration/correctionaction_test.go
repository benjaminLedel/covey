package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"covey/internal/backlog"
)

// The second source of correction pairs (spec/24): an agent notices that a
// person rewrote a text it had published, and files the pair itself.
//
// It has to be the agent. A plugin cannot do it — it runs in the sandbox with
// its own target system's credential and has no way back to the platform — and
// the control plane cannot see into a target system at all. The agent is the
// only party that can both read what stands there now and speak to covey.
func TestAnAgentFilesTheCorrectionItFoundInATargetSystem(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("schreiber")

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

	entwurf := "Wir möchten Sie darüber in Kenntnis setzen, dass im Rahmen der Durchführung " +
		"der Migration eine Verzögerung eingetreten ist, welche voraussichtlich zu einer " +
		"Verschiebung des vereinbarten Termins führen wird."
	korrigiert := "Die Migration verzögert sich. Der Termin am 14. März fällt weg, einen neuen " +
		"nennen wir bis Freitag. Der Grund liegt an der Datenbank, die zuerst umziehen muss."

	fileIt := func(params map[string]any) backlog.Task {
		t.Helper()
		raw, _ := json.Marshal(params)
		task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Nachsehen",
			"[mock:action covey/correction "+string(raw)+"]\n[mock:result nachgesehen]", "manual", 3)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the run is over", 30*time.Second, func() bool {
			st := s.taskState(task.ID)
			return st == backlog.StateDone || st == backlog.StateFailed
		})
		return task
	}

	// A person rewrote it: the pair belongs to the voice.
	fileIt(map[string]any{"before": entwurf, "after": korrigiert,
		"where": "zammad:reply_external", "by": "Frau Weber"})
	pairs := admin.expectList(http.MethodGet, "/api/v1/voices/"+voiceID+"/corrections", nil, http.StatusOK)
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %d", len(pairs))
	}
	if pairs[0]["source"] != "target" || pairs[0]["action"] != "zammad:reply_external" {
		t.Fatalf("the origin belongs to the pair: %+v", pairs[0])
	}

	// A colleague rewrote it: that is a handover. A voice that learns from it
	// learns to imitate itself.
	kollege := s.newSupportAgent("lektor")
	fileIt(map[string]any{"before": entwurf, "after": korrigiert,
		"where": "zammad:reply_external", "by": kollege.Slug})

	// Nobody said who: refused, because the question decides whether this is a
	// correction at all.
	fileIt(map[string]any{"before": entwurf, "after": korrigiert, "where": "zammad:reply_external"})

	// A typo is not a correction.
	fileIt(map[string]any{"before": entwurf, "after": entwurf + " Nachtrag.",
		"where": "zammad:reply_external", "by": "Frau Weber"})

	if again := admin.expectList(http.MethodGet, "/api/v1/voices/"+voiceID+"/corrections",
		nil, http.StatusOK); len(again) != 1 {
		t.Fatalf("only the person's real rewrite may be stored, got %d pairs", len(again))
	}

	// And it stands in the recording: a pair steers how every agent on this
	// voice writes, so it has to be findable without opening the voice.
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='lifecycle' AND payload->>'status'='correction'`,
		agent.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("the recording holds %d correction events, expected 1", n)
	}
}

// An agent without a voice has nowhere to put a pair, and is told so rather
// than having it stored against nothing.
func TestAnAgentWithoutAVoiceIsToldWhereToPutIt(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("stimmlos")

	raw, _ := json.Marshal(map[string]any{
		"before": "Wir möchten Sie darüber in Kenntnis setzen, dass die Migration sich verzögert " +
			"und der Termin verschoben werden muss, was wir sehr bedauern.",
		"after": "Die Migration verzögert sich. Der Termin am 14. März fällt weg, einen neuen " +
			"nennen wir bis Freitag.",
		"by": "Frau Weber"})
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Nachsehen",
		"[mock:action covey/correction "+string(raw)+"]\n[mock:result nachgesehen]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run is over", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='lifecycle' AND payload->>'status'='correction'`,
		agent.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("nothing may be stored for an agent without a voice, got %d", n)
	}
}
