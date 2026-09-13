package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

// The recording is the documented way to find out why a run went wrong — and at
// a failed task it showed `lifecycle: {"status":"task_failed"}` and nothing
// else. The reason stood only in the task's `error` field, so whoever followed
// the documented path saw that something had failed and not what (#221). At a
// heartbeat agent the same silence repeats every interval.
//
// So the reason travels with the status.
func TestAFailedRunRecordsItsReason(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("support")

	// The text is the one the missing engine binary produced, because that is
	// the case this comes from: an error that names a file nobody can find is
	// exactly the sentence somebody searches the recording for.
	reason := `sevencode could not be run (exec: "sevencode": executable file not found in $PATH)`
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Scheitert",
		"[mock:fail "+reason+"]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task failed", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateFailed
	})

	events, err := s.obs.Events(ctx, agent.ID, &task.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.Kind != "lifecycle" {
			continue
		}
		var p struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil || p.Status != "task_failed" {
			continue
		}
		found = true
		if !strings.Contains(p.Error, "executable file not found") {
			t.Fatalf("the lifecycle event carries no usable reason: %s", e.Payload)
		}
	}
	if !found {
		t.Fatalf("no task_failed event in %d recorded events", len(events))
	}
}
