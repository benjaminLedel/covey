package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"covey/internal/observability"
)

// Costs per RUN, not only per day.
//
// The costs were bookable per task from the start, but the number never came
// back out: whoever wanted to know where an expensive day came from had to
// correlate cost buckets against the backlog by hand over timestamps. The
// ranking answers it directly — and carries the action count alongside, because
// an expensive run that changed nothing looks in every aggregate exactly like an
// expensive one that fixed three bugs.
func TestRunCostsRankRunsAndExposeIdleOnes(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent, err := s.registry.Create(ctx, s.orgID, "kostenprobe", "Kostenprobe", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}

	// Two runs: the expensive one changed nothing, the cheap one acted.
	leer, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Heartbeat ohne Befund", "", "heartbeat", 0)
	if err != nil {
		t.Fatal(err)
	}
	arbeit, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Issue #7 behoben", "", "heartbeat", 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.obs.AddCost(ctx, agent.ID, &leer.ID, 4.50,
		observability.Tokens{Input: 10, Output: 2000, CacheRead: 3_000_000}, "claude-opus-5"); err != nil {
		t.Fatal(err)
	}
	if err := s.obs.AddCost(ctx, agent.ID, &arbeit.ID, 1.25,
		observability.Tokens{Input: 5, Output: 900, CacheRead: 700_000}, "claude-opus-5"); err != nil {
		t.Fatal(err)
	}
	// Only the second run touched a target system.
	for i := 0; i < 3; i++ {
		if err := s.obs.Record(ctx, s.orgID, agent.ID, &arbeit.ID, observability.KindAction,
			map[string]string{"tool": "gitlab:comment"}); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := s.obs.RunCosts(ctx, s.orgID, nil, time.Now().Add(-time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected both runs, got %d: %+v", len(runs), runs)
	}
	// Costliest first — that is what the list is for.
	if runs[0].TaskID != leer.ID {
		t.Fatalf("the expensive run belongs at the top, got %q", runs[0].Title)
	}
	if runs[0].TotalUSD < runs[1].TotalUSD {
		t.Fatalf("not sorted by cost: %+v", runs)
	}
	// The idle signal: the expensive run changed nothing.
	if runs[0].Actions != 0 {
		t.Fatalf("the run without actions must show actions=0, got %d", runs[0].Actions)
	}
	if runs[1].Actions != 3 {
		t.Fatalf("the acting run must show its 3 actions, got %d", runs[1].Actions)
	}
	// The cost breakdown comes along — cache-read is what carries the weight,
	// and it is invisible in the USD figure alone.
	if runs[0].CacheRead != 3_000_000 {
		t.Fatalf("cache-read tokens belong on the run: %+v", runs[0])
	}
	if runs[0].Slug != "kostenprobe" || runs[0].Origin != "heartbeat" {
		t.Fatalf("agent and origin belong on the run: %+v", runs[0])
	}

	// Filtering by agent narrows it; a foreign agent gets nothing.
	fremd, err := s.registry.Create(ctx, s.orgID, "fremd", "Fremd", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	nur, err := s.obs.RunCosts(ctx, s.orgID, &fremd.ID, time.Now().Add(-time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(nur) != 0 {
		t.Fatalf("an agent without runs has no costs: %+v", nur)
	}

	// The window cuts: nothing was booked before the last hour.
	alt, err := s.obs.RunCosts(ctx, s.orgID, nil, time.Now().Add(time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(alt) != 0 {
		t.Fatalf("the time window has to cut: %+v", alt)
	}
}

// The same list over the API, org-wide and per agent.
func TestRunCostsOverAPI(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	agent, err := s.registry.Create(ctx, s.orgID, "api-kosten", "API Kosten", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Ein Lauf", "", "heartbeat", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.obs.AddCost(ctx, agent.ID, &task.ID, 2.00,
		observability.Tokens{Output: 100, CacheRead: 500_000}, "claude-opus-5"); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/v1/cost/runs?days=1",
		"/api/v1/agents/" + agent.ID.String() + "/cost/runs?days=1",
	} {
		// expect() decodes an object; these endpoints answer with a list.
		resp := admin.do(http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("%s: HTTP %d", path, resp.StatusCode)
		}
		var runs []observability.RunCost
		err := json.NewDecoder(resp.Body).Decode(&runs)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(runs) != 1 || runs[0].TaskID != task.ID {
			t.Fatalf("%s: expected the one run, got %+v", path, runs)
		}
		if runs[0].TotalUSD != 2 {
			t.Fatalf("%s: cost wrong: %+v", path, runs[0])
		}
	}
}

// The cost hangs on the task in the backlog itself.
//
// The ranking answers "which run was expensive", but whoever looks at a task
// asks the question the other way round: what did THIS one cost? Without the
// number on the card that means a detour via the cost page and matching by
// title. A task that has not run yet carries no zero — it carries nothing.
func TestBacklogCarriesCostPerTask(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	agent, err := s.registry.Create(ctx, s.orgID, "backlog-kosten", "Backlog Kosten", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	gelaufen, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Hat gearbeitet", "", "heartbeat", 0)
	if err != nil {
		t.Fatal(err)
	}
	offen, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Wartet noch", "", "manual:test", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Two turns of one run — the card shows the sum, not the last booking.
	for _, usd := range []float64{0.75, 0.25} {
		if err := s.obs.AddCost(ctx, agent.ID, &gelaufen.ID, usd,
			observability.Tokens{Output: 100}, "claude-opus-5"); err != nil {
			t.Fatal(err)
		}
	}

	resp := admin.do(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/backlog", nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("backlog: HTTP %d", resp.StatusCode)
	}
	var tasks []struct {
		ID          string   `json:"id"`
		CostUSD     *float64 `json:"cost_usd"`
		CostEntries int64    `json:"cost_entries"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tasks)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected both tasks, got %+v", tasks)
	}
	for _, tk := range tasks {
		switch tk.ID {
		case gelaufen.ID.String():
			if tk.CostUSD == nil || *tk.CostUSD != 1.00 {
				t.Fatalf("the run's two bookings have to add up to 1.00: %+v", tk)
			}
			if tk.CostEntries != 2 {
				t.Fatalf("the number of bookings belongs on the task: %+v", tk)
			}
		case offen.ID.String():
			if tk.CostUSD != nil {
				t.Fatalf("a task that has not run carries no cost, not a zero: %+v", tk)
			}
		default:
			t.Fatalf("unknown task in the backlog: %+v", tk)
		}
	}
}
