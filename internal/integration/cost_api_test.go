package integration

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/observability"
)

// Cost is what an organisation actually decides on: per agent, per model, per
// credential, over time. The report is assembled from four queries, and the
// one thing that must hold across them is that they describe the SAME window —
// a total over thirty days beside a series over seven would not be wrong in
// any one place and would still be a lie.
func TestOrgCostReportOverTheAPI(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("kosten-bericht")

	// Two entries, one of them outside a seven-day window.
	if err := s.obs.AddCost(ctx, agent.ID, nil, 1.25,
		observability.Tokens{Input: 1000, Output: 200}, "claude-opus-5", uuid.Nil, -1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO cost_entries (agent_id, model, usd, input_tokens, output_tokens, created_at)
		 VALUES ($1, 'claude-opus-5', 9.00, 100, 10, now() - interval '20 days')`,
		agent.ID); err != nil {
		t.Fatal(err)
	}

	all := admin.expect(http.MethodGet, "/api/v1/cost/org?days=30", nil, http.StatusOK)
	total, _ := all["total_usd"].(float64)
	if total < 10 {
		t.Errorf("the thirty-day total is %v, expected both entries", total)
	}

	// The window is what the ?days= says, and the total follows it — otherwise
	// the tiles and the chart would disagree about the same page.
	week := admin.expect(http.MethodGet, "/api/v1/cost/org?days=7", nil, http.StatusOK)
	weekTotal, _ := week["total_usd"].(float64)
	if weekTotal >= total {
		t.Errorf("the seven-day total (%v) is not smaller than the thirty-day one (%v)", weekTotal, total)
	}

	// The buckets are the chart's x-axis. An unknown one falls back rather than
	// producing an empty chart nobody can explain.
	for _, bucket := range []string{"", "day", "week", "month", "erfunden"} {
		admin.expect(http.MethodGet, "/api/v1/cost/org?bucket="+bucket, nil, http.StatusOK)
	}
	// A window beyond the cap is capped rather than refused — somebody asking
	// for five years wants "everything", not an error.
	admin.expect(http.MethodGet, "/api/v1/cost/org?days=100000", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/cost/org?days=keine-zahl", nil, http.StatusOK)

	// Per agent: the same numbers, narrowed.
	perAgent := admin.expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/cost", nil, http.StatusOK)
	if perAgent["total_usd"] == nil {
		t.Errorf("the agent's cost carries no total: %v", perAgent)
	}
	admin.expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/cost/series?days=7&bucket=day", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/cost/runs?limit=5", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/cost/runs?limit=5&days=7", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/cost/indicators", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/cost/indicators", nil, http.StatusOK)
}

// The breakdown answers "who is costing what", so an agent that has never run
// is left out rather than listed at zero — a page of zeroes is a page nobody
// reads, and the agent list next door already says who exists.
func TestCostBreakdownLeavesOutWhatCostNothing(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	idle := s.newSupportAgent("nichtstuer")
	busy := s.newSupportAgent("vielarbeiter")

	if err := s.obs.AddCost(ctx, busy.ID, nil, 2.50,
		observability.Tokens{Input: 500}, "claude-opus-5", uuid.Nil, -1); err != nil {
		t.Fatal(err)
	}

	rep := admin.expect(http.MethodGet, "/api/v1/cost/org", nil, http.StatusOK)
	agentsList, _ := rep["agents"].([]any)
	var sawBusy, sawIdle bool
	for _, a := range agentsList {
		m, _ := a.(map[string]any)
		if m == nil {
			continue
		}
		switch m["slug"] {
		case busy.Slug:
			sawBusy = true
			if usd, _ := m["total_usd"].(float64); usd != 2.50 {
				t.Errorf("the working agent costs %v, expected 2.50", usd)
			}
		case idle.Slug:
			sawIdle = true
		}
	}
	if !sawBusy {
		t.Errorf("the agent that cost something is missing: %v", agentsList)
	}
	if sawIdle {
		t.Error("an agent that never ran was listed at zero")
	}

	// The model breakdown is the other axis of the same total.
	models, _ := rep["models"].([]any)
	if len(models) == 0 {
		t.Errorf("the report names no model: %v", rep)
	}
}

// The recording is what makes a run readable afterwards. Its filters are the
// only way anybody gets at a long one, so they are part of the contract.
func TestRecordingFiltersOverTheAPI(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("aufzeichnung-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/recording"

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Aufzeichnung", "[mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == "done"
	})

	all := admin.expectList(http.MethodGet, base, nil, http.StatusOK)
	if len(all) == 0 {
		t.Fatal("a finished run left no recording")
	}

	// Narrowed to one task: the same events, without the rest of the agent's
	// history.
	perTask := admin.expectList(http.MethodGet, base+"?task_id="+task.ID.String(), nil, http.StatusOK)
	if len(perTask) == 0 {
		t.Error("the task's own recording is empty")
	}
	if len(perTask) > len(all) {
		t.Errorf("one task carries more events (%d) than the whole agent (%d)", len(perTask), len(all))
	}

	// ?after= is how the live view follows along: everything since a sequence
	// number it already has. Past the end that is an empty list, not an error —
	// the view polls, and an error would be a red banner on a quiet agent.
	var last float64
	for _, e := range all {
		if seq, ok := e["id"].(float64); ok && seq > last {
			last = seq
		}
	}
	rest := admin.expectList(http.MethodGet, base+"?after="+itoa(int64(last)), nil, http.StatusOK)
	if len(rest) != 0 {
		t.Errorf("?after= the last event returned %d more", len(rest))
	}

	admin.expect(http.MethodGet, base+"?task_id=keine-uuid", nil, http.StatusBadRequest)
	admin.expect(http.MethodGet, "/api/v1/agents/keine-uuid/recording", nil, http.StatusBadRequest)
	admin.expect(http.MethodGet, "/api/v1/recordings/blobs/keine-uuid", nil, http.StatusBadRequest)
	admin.expect(http.MethodGet, "/api/v1/recordings/blobs/"+uuid.NewString(), nil, http.StatusNotFound)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
