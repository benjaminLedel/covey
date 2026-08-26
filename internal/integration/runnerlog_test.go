package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/runner"
	runnerstore "covey/internal/runner/store"
)

// What a runner says has to arrive where the runner is administered. This
// test drives the path that breaks at runtime and not at compile time: the
// batch INSERT via unnest, the filters of the query, and the route the
// interface reads.
func TestRunnerLogIsStoredAndRead(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	tokens := runnerstore.NewBuiltinTokens(s.runners)
	runnerID, _, err := tokens.For(ctx, s.orgID)
	if err != nil {
		t.Fatalf("built-in runner: %v", err)
	}

	agentID := uuid.New()
	now := time.Now()
	err = s.runners.AppendLogs(ctx, s.orgID, runnerID, []runner.LogEntry{
		{Time: now.Add(-2 * time.Second), Level: "info", Msg: "sandbox start requested",
			Attrs: map[string]string{"image": "covey-sandbox:test"}, AgentID: agentID},
		{Time: now.Add(-time.Second), Level: "debug", Msg: "runner: message received"},
		{Time: now, Level: "error", Msg: "docker start failed",
			Attrs: map[string]string{"err": "no space left on device"}},
	})
	if err != nil {
		t.Fatalf("AppendLogs: %v", err)
	}

	// Without a filter everything comes back, newest first.
	all := c.expectList(http.MethodGet, "/api/v1/runners/"+runnerID.String()+"/logs", nil, http.StatusOK)
	if len(all) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(all), all)
	}
	if all[0]["msg"] != "docker start failed" {
		t.Fatalf("not newest first: %v", all[0]["msg"])
	}
	attrs, _ := all[0]["attrs"].(map[string]any)
	if attrs["err"] != "no space left on device" {
		t.Fatalf("attributes did not survive the trip: %v", all[0]["attrs"])
	}

	// level=info leaves the debug line out — that is the READER's filter.
	onlyInfo := c.expectList(http.MethodGet, "/api/v1/runners/"+runnerID.String()+"/logs?level=info", nil, http.StatusOK)
	if len(onlyInfo) != 2 {
		t.Fatalf("level=info must leave the debug line out, got %d", len(onlyInfo))
	}

	// And a start's line can be narrowed to its agent without anybody parsing
	// text.
	perAgent := c.expectList(http.MethodGet,
		"/api/v1/runners/"+runnerID.String()+"/logs?agent="+agentID.String(), nil, http.StatusOK)
	if len(perAgent) != 1 || perAgent[0]["msg"] != "sandbox start requested" {
		t.Fatalf("filtering by agent does not hold: %v", perAgent)
	}
}

// The level belongs on the runner's row and not only in the message: a host
// that drops out for a minute has to come back at the level the interface
// shows.
func TestLogLevelIsStoredOnTheRow(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")
	ctx := context.Background()

	tokens := runnerstore.NewBuiltinTokens(s.runners)
	runnerID, _, err := tokens.For(ctx, s.orgID)
	if err != nil {
		t.Fatalf("built-in runner: %v", err)
	}

	// An unknown level is refused rather than silencing a host.
	c.expect(http.MethodPost, "/api/v1/runners/"+runnerID.String()+"/log-level",
		map[string]string{"level": "warn"}, http.StatusBadRequest)

	c.expect(http.MethodPost, "/api/v1/runners/"+runnerID.String()+"/log-level",
		map[string]string{"level": "debug"}, http.StatusOK)

	rn, err := s.runners.ByID(ctx, runnerID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if rn.LogLevel != "debug" {
		t.Fatalf("the level is not on the row: %q", rn.LogLevel)
	}
}
