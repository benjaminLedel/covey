package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestPlatformPromptFollowsBinary is the rollout guarantee for existing
// installations: the platform part of the system prompt (completion protocol,
// covey/ meta actions, stage rules) comes from the binary, not from the config
// version frozen at save time.
//
// Without it, every productive agent config would have to be saved again by hand
// after each deploy just so the agent learns about a new action at all — and an
// agent nobody has touched for months would keep running with the platform
// contract from its last config edit.
//
// The test simulates exactly that: it overwrites the stored compiled_prompt with
// an outdated state and checks that the agent still gets the current one —
// including its own SOUL.md.
func TestPlatformPromptFollowsBinary(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("altbestand")

	// State of an installation that last saved before the deploy.
	tag, err := s.pool.Exec(ctx,
		"UPDATE agent_config_versions SET compiled_prompt=$2 WHERE agent_id=$1",
		agent.ID, "# OUTDATED PROMPT\n\nKnows neither create_task nor stage rules.")
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatal("no stored prompt was overwritten — the test would prove nothing")
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Show the prompt", "[mock:prompt]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result == nil {
		t.Fatal("the run has to deliver the system prompt")
	}
	prompt := *got.Result

	if strings.Contains(prompt, "OUTDATED PROMPT") {
		t.Fatalf("the frozen compiled_prompt must no longer carry the run")
	}
	// Platform part: current state from the binary.
	for _, want := range []string{"covey/create_task", "covey/set_stage", "COVEY_STATUS"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the platform protocol is incomplete — %q is missing", want)
		}
	}
	// Agent part: its own config is still in there.
	if !strings.Contains(prompt, "Support-Agent") {
		t.Fatalf("the SOUL.md of the agent has to be in the prompt")
	}
}
