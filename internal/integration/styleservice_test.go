package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestStyleCheckIsAPlatformService: an agent hands a draft to covey/style_check
// and gets the findings back, without Python or a skill in its workplace, and
// without any scope in ACCESS.md — measuring changes nothing.
func TestStyleCheckIsAPlatformService(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("messer")
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
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Entwurf messen",
		"[mock:action covey/style_check "+string(params)+"]\n[mock:result gemessen]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='action' AND payload->>'action'='covey:style_check' AND (payload->>'text_chars')::int > 100`,
		agent.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected one recorded style_check action, got %d", n)
	}
}

// TestStyleApplyWithoutModelSaysSo: revising needs the organisation's model;
// without a credential the action fails with a reason, not halfway.
func TestStyleApplyWithoutModelSaysSo(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("umschreiber")
	params, _ := json.Marshal(map[string]any{"text": styleGenericBody})
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Entwurf umschreiben",
		"[mock:action covey/style_apply "+string(params)+"]\n[mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task ends", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	if st := s.taskState(task.ID); st != backlog.StateFailed {
		t.Fatalf("without a model the action is an error the runtime sees; task state %s", st)
	}
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "LLM credential") {
		t.Fatalf("the reason has to reach the task's error: %v", got.Error)
	}
}
