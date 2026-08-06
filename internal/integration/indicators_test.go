package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"covey/internal/httpapi"
	"covey/internal/observability"
)

// aktion records an executed target-system action the way the action proxy
// does it: action name, params, and whether it worked.
func aktion(t *testing.T, s *stack, agentID, taskID uuid.UUID, action string, params map[string]any, ok bool) {
	t.Helper()
	payload := map[string]any{"action": action, "params": params, "ok": ok}
	if err := s.obs.Record(context.Background(), s.orgID, agentID, &taskID, observability.KindAction, payload); err != nil {
		t.Fatal(err)
	}
}

// nahe compares money with a tolerance. The stack under test runs a real
// orchestrator: open tasks get dispatched to the mock runtime, which books a
// few tenths of a cent of its own. Checking to the cent would make these tests
// depend on whether the dispatcher got there first — which has nothing to do
// with what they assert.
func nahe(a, b float64) bool { return a-b < 0.5 && b-a < 0.5 }

func indicatorReport(t *testing.T, c *apiClient, path string) httpapi.IndicatorReport {
	t.Helper()
	var rep httpapi.IndicatorReport
	resp := c.do(http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: HTTP %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return rep
}

// What the workforce delivered, and what a unit of it cost.
//
// The decisive property is `je:`: five replies in the same ticket are ONE
// resolved ticket. Counted raw, a chatty agent's unit cost drops to a fifth and
// it looks like the best employee in the building.
func TestIndicatorsCountObjectsNotEvents(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	agent, err := s.registry.Create(ctx, s.orgID, "kennzahl-probe", "Kennzahl Probe", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md": "Du bist ein Support-Agent.",
		"KPIS.md": "- kennzahl: geloeste-tickets titel: Gelöste Tickets zählt: aktion zammad:reply_external je: ticket_id\n" +
			"- kennzahl: roh titel: Antworten roh zählt: aktion zammad:reply_external",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Tickets bearbeiten", "", "heartbeat", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Five successful replies across two tickets — plus one that failed, and
	// one action of a kind nobody counts.
	for i := 0; i < 4; i++ {
		aktion(t, s, agent.ID, task.ID, "zammad:reply_external", map[string]any{"ticket_id": 4711}, true)
	}
	aktion(t, s, agent.ID, task.ID, "zammad:reply_external", map[string]any{"ticket_id": 4712}, true)
	aktion(t, s, agent.ID, task.ID, "zammad:reply_external", map[string]any{"ticket_id": 4713}, false)
	aktion(t, s, agent.ID, task.ID, "zammad:get_ticket", map[string]any{"ticket_id": 4711}, true)

	if err := s.obs.AddCost(ctx, agent.ID, &task.ID, 6.00,
		observability.Tokens{Output: 500}, "claude-opus-5"); err != nil {
		t.Fatal(err)
	}

	rep := indicatorReport(t, admin, "/api/v1/agents/"+agent.ID.String()+"/cost/indicators?days=1")
	byKey := map[string]observability.IndicatorResult{}
	for _, ind := range rep.Indicators {
		byKey[ind.Key] = ind
	}
	if len(byKey) != 2 {
		t.Fatalf("expected both indicators, got %+v", rep.Indicators)
	}
	// Two tickets, not five replies. The failed reply counts for neither.
	if got := byKey["geloeste-tickets"].Count; got != 2 {
		t.Errorf("je: ticket_id has to count objects: expected 2 tickets, got %d", got)
	}
	if got := byKey["roh"].Count; got != 5 {
		t.Errorf("without je: the successful events count: expected 5, got %d", got)
	}
	// Below the minimum count there is no price — two events are not a
	// measurement.
	if byKey["geloeste-tickets"].UnitUSD != nil {
		t.Errorf("2 events must not carry a unit cost: %+v", byKey["geloeste-tickets"])
	}
	// Five do, and the denominator is the agent's entire cost — including what
	// it burned without delivering.
	if u := byKey["roh"].UnitUSD; u == nil || !nahe(*u, 1.20) {
		t.Errorf("unit cost wrong: %+v (unit=%v)", byKey["roh"], byKey["roh"].UnitUSD)
	}
	if !nahe(rep.TotalUSD, 6) {
		t.Errorf("the denominator belongs in the answer: %+v", rep)
	}
}

// The org-wide grouping by key, and the counter-figure next to the prices.
func TestIndicatorsGroupByKeyAndCountFailures(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Two agents carry the same key.
	for _, slug := range []string{"support-a", "support-b"} {
		a, err := s.registry.Create(ctx, s.orgID, slug, slug, "mock", &s.adminID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.registry.SaveConfig(ctx, a.ID, map[string]string{
			"KPIS.md": "- kennzahl: geloeste-tickets titel: Gelöste Tickets zählt: aktion zammad:reply_external je: ticket_id",
		}, &s.adminID); err != nil {
			t.Fatal(err)
		}
		task, err := s.backlog.Create(ctx, s.orgID, a.ID, "Arbeit", "", "heartbeat", 0)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			aktion(t, s, a.ID, task.ID, "zammad:reply_external",
				map[string]any{"ticket_id": fmt.Sprintf("%s-%d", slug, i)}, true)
		}
		if err := s.obs.AddCost(ctx, a.ID, &task.ID, 3.00, observability.Tokens{}, "claude-opus-5"); err != nil {
			t.Fatal(err)
		}
	}

	// An agent without indicators that burns money and fails: it must not
	// lower the price of a ticket, but its failure has to show.
	stumm, err := s.registry.Create(ctx, s.orgID, "ohne-kennzahl", "Ohne", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	kaputt, err := s.backlog.Create(ctx, s.orgID, stumm.ID, "Geht schief", "", "heartbeat", 0)
	if err != nil {
		t.Fatal(err)
	}
	// A task only reaches `failed` from `in_progress` — the state machine has
	// no shortcut, and neither does the test.
	if _, err := s.backlog.ClaimNext(ctx, stumm.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, kaputt.ID, "failed", "", "kaputt"); err != nil {
		t.Fatal(err)
	}
	if err := s.obs.AddCost(ctx, stumm.ID, &kaputt.ID, 90.00, observability.Tokens{}, "claude-opus-5"); err != nil {
		t.Fatal(err)
	}

	rep := indicatorReport(t, admin, "/api/v1/cost/indicators?days=1")
	if len(rep.Indicators) != 1 {
		t.Fatalf("both agents share one key — expected one line: %+v", rep.Indicators)
	}
	ind := rep.Indicators[0]
	if ind.Count != 6 {
		t.Errorf("the key is summed across its agents: expected 6, got %d", ind.Count)
	}
	// The denominator is the cost of the CARRIERS (2 × 3 $), not of everybody:
	// the 90 $ of the agent without the key have nothing to do with the price
	// of a ticket.
	// 1.00 $, not 16.00 $ — that is the whole point of the carrier set.
	if u := ind.UnitUSD; u == nil || !nahe(*u, 1.00) {
		t.Errorf("only the carriers belong in the denominator: %+v (unit=%v)", ind, ind.UnitUSD)
	}
	if rep.Failed != 1 {
		t.Errorf("the failed run belongs next to the prices: %+v", rep)
	}
	// The org-wide total does include everything — it is the denominator shown
	// for checking, not a price.
	if !nahe(rep.TotalUSD, 96) {
		t.Errorf("org total wrong: %+v", rep)
	}
}

// The rework rate: did the case come back?
//
// A ticket resolved today and worked on again on Thursday was not resolved, and
// in the price list it counts as delivery all the same. Recognised by two
// DIFFERENT runs acting on the same object — the task's correlation_key cannot
// serve, because Complete clears it when a task finishes; a completed task no
// longer knows which ticket it was.
func TestIndicatorsCountReturnedCases(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	agent, err := s.registry.Create(ctx, s.orgID, "rueckl", "Rückläufer", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"KPIS.md": "- kennzahl: tickets titel: Tickets zählt: aktion zammad:reply_external je: ticket_id\n" +
			"- kennzahl: ohne-je titel: Ohne Objekt zählt: aktion zammad:reply_external",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	// Three tickets are answered in a first run …
	erster, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Erster Lauf", "", "webhook", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ticket := range []int{1, 2, 3} {
		aktion(t, s, agent.ID, erster.ID, "zammad:reply_external", map[string]any{"ticket_id": ticket}, true)
	}
	// … and one of them comes back: a second run has to touch it again.
	zweiter, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Kunde meldet sich erneut", "", "webhook", 0)
	if err != nil {
		t.Fatal(err)
	}
	aktion(t, s, agent.ID, zweiter.ID, "zammad:reply_external", map[string]any{"ticket_id": 1}, true)

	rep := indicatorReport(t, admin, "/api/v1/agents/"+agent.ID.String()+"/cost/indicators?days=1")
	byKey := map[string]observability.IndicatorResult{}
	for _, ind := range rep.Indicators {
		byKey[ind.Key] = ind
	}
	// Three tickets — the fourth reply was the same ticket again.
	if got := byKey["tickets"].Count; got != 3 {
		t.Errorf("expected 3 tickets, got %d", got)
	}
	if got := byKey["tickets"].Returned; got != 1 {
		t.Errorf("exactly one case came back: %d (%+v)", got, byKey["tickets"])
	}
	// Without `je:` there is no object identity — and then no invented zero
	// either, which would claim a quality nobody measured.
	if got := byKey["ohne-je"].Returned; got != 0 {
		t.Errorf("without je: no rework rate may be claimed: %+v", byKey["ohne-je"])
	}
}

// The trend: the same span again, directly before — and the course over the
// period.
//
// The previous period has to be measured against ITS OWN cost. Dividing the old
// count by today's cost would report a change in price that never happened.
func TestIndicatorsCarryTrendAndSeries(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	agent, err := s.registry.Create(ctx, s.orgID, "trend", "Trend", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"KPIS.md": "- kennzahl: tickets titel: Tickets zaehlt: aktion zammad:reply_external je: ticket_id",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Arbeit", "", "webhook", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Four tickets now …
	for i := 1; i <= 4; i++ {
		aktion(t, s, agent.ID, task.ID, "zammad:reply_external", map[string]any{"ticket_id": i}, true)
	}
	// … and two in the previous period, backdated past the window boundary.
	// ?days=1 means: the last 24 hours against the 24 before them.
	for i := 5; i <= 6; i++ {
		aktion(t, s, agent.ID, task.ID, "zammad:reply_external", map[string]any{"ticket_id": i}, true)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE recording_events SET created_at = now() - interval '30 hours'
		WHERE agent_id=$1 AND payload->'params'->>'ticket_id' IN ('5','6')`, agent.ID); err != nil {
		t.Fatal(err)
	}

	rep := indicatorReport(t, admin, "/api/v1/agents/"+agent.ID.String()+"/cost/indicators?days=1")
	if len(rep.Indicators) != 1 {
		t.Fatalf("expected the one indicator: %+v", rep.Indicators)
	}
	ind := rep.Indicators[0]
	if ind.Count != 4 {
		t.Errorf("the current period holds four tickets: %d", ind.Count)
	}
	if ind.PrevCount != 2 {
		t.Errorf("the previous period holds two: %d", ind.PrevCount)
	}
	// The course covers the current period only, and its buckets add up to what
	// happened in it — with one bucket carrying everything, since the test
	// writes it all at once.
	if len(ind.Series) == 0 {
		t.Fatalf("the sparkline needs points: %+v", ind)
	}
	var summe int64
	for _, v := range ind.Series {
		summe += v
	}
	if summe != 4 {
		t.Errorf("the course must not contain the previous period: %v (sum %d)", ind.Series, summe)
	}
}
