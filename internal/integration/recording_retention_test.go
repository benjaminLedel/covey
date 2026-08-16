package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"covey/internal/observability"
)

// How long the verbatim record is kept (spec/06). Three things have to hold, and
// each of them deletes an audit trail if it does not: only the transcript
// expires, an agent may extend the organisation's window but never shorten it,
// and zero means forever rather than nothing.

// altern moves an event back in time. Retention is measured in days, and a test
// that waited for one would not be a test.
func altern(t *testing.T, s *stack, agentID uuid.UUID, tage int) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE recording_events SET created_at = now() - make_interval(days => $2)
		  WHERE agent_id = $1`, agentID, tage); err != nil {
		t.Fatal(err)
	}
}

func zaehle(t *testing.T, s *stack, agentID uuid.UUID, kind string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM recording_events WHERE agent_id = $1 AND kind = $2`,
		agentID, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// schreibeVerlaufUndAktion writes one of each: the transcript that expires and
// the action that does not.
func schreibeVerlaufUndAktion(t *testing.T, s *stack, agentID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := s.obs.Record(ctx, s.orgID, agentID, nil, observability.KindRuntime,
		map[string]any{"text": "der wörtliche Verlauf"}); err != nil {
		t.Fatal(err)
	}
	if err := s.obs.Record(ctx, s.orgID, agentID, nil, observability.KindAction,
		map[string]any{"action": "gitlab:merge_mr", "ok": true}); err != nil {
		t.Fatal(err)
	}
}

func TestNurDerVerlaufVerfaellt(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	agent := s.newSupportAgent("verfall-agent")
	schreibeVerlaufUndAktion(t, s, agent.ID)
	altern(t, s, agent.ID, 400) // älter als die Vorgabe von 365 Tagen

	n, err := s.obs.CleanupRecordings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("exactly the one transcript should have gone: %d", n)
	}
	if got := zaehle(t, s, agent.ID, "runtime"); got != 0 {
		t.Errorf("the transcript survived its retention: %d", got)
	}
	// The one that matters: an action event is the audit trail and the basis of
	// every indicator (spec/17). A retention that catches it is a breaking
	// change, not a setting.
	if got := zaehle(t, s, agent.ID, "action"); got != 1 {
		t.Errorf("the action event must never expire: %d", got)
	}
}

func TestFrischerVerlaufBleibt(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	agent := s.newSupportAgent("frisch-agent")
	schreibeVerlaufUndAktion(t, s, agent.ID)
	altern(t, s, agent.ID, 10)

	if _, err := s.obs.CleanupRecordings(ctx); err != nil {
		t.Fatal(err)
	}
	if got := zaehle(t, s, agent.ID, "runtime"); got != 1 {
		t.Errorf("ten days are not a year: %d", got)
	}
}

// The direction is the whole rule: an agent may keep LONGER than the
// organisation requires, never shorter. An agent that could shorten its own
// trail would be exactly the gap the org-wide setting exists to close.
func TestAgentDarfNurVerlaengern(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)

	kurz := s.newSupportAgent("kurz-agent")
	lang := s.newSupportAgent("lang-agent")
	schreibeVerlaufUndAktion(t, s, kurz.ID)
	schreibeVerlaufUndAktion(t, s, lang.ID)
	altern(t, s, kurz.ID, 100)
	altern(t, s, lang.ID, 100)

	// Die Organisation hält 90 Tage.
	if err := s.obs.SetOrgRecordingRetention(ctx, s.orgID, 90); err != nil {
		t.Fatal(err)
	}
	// Der eine will kürzer (30) — das darf nichts bewirken.
	dreissig := 30
	if err := s.registry.SetRecordingRetention(ctx, kurz.ID, &dreissig); err != nil {
		t.Fatal(err)
	}
	// Der andere will länger (365) — das gilt.
	dreihundert := 365
	if err := s.registry.SetRecordingRetention(ctx, lang.ID, &dreihundert); err != nil {
		t.Fatal(err)
	}

	if _, err := s.obs.CleanupRecordings(ctx); err != nil {
		t.Fatal(err)
	}
	// 100 Tage alt, Organisation hält 90: weg — die 30 des Agenten verkürzen nicht.
	if got := zaehle(t, s, kurz.ID, "runtime"); got != 0 {
		t.Errorf("an agent must not undercut the organisation: %d left", got)
	}
	// 100 Tage alt, Agent hält 365: bleibt.
	if got := zaehle(t, s, lang.ID, "runtime"); got != 1 {
		t.Errorf("the agent's longer window has to apply: %d", got)
	}
}

// Zero reads like "keep nothing" and means the opposite. If that is ever got
// wrong, it is got wrong in the direction that deletes everything.
func TestNullBedeutetUnbegrenzt(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	agent := s.newSupportAgent("ewig-agent")
	schreibeVerlaufUndAktion(t, s, agent.ID)
	altern(t, s, agent.ID, 5000)

	if err := s.obs.SetOrgRecordingRetention(ctx, s.orgID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.obs.CleanupRecordings(ctx); err != nil {
		t.Fatal(err)
	}
	if got := zaehle(t, s, agent.ID, "runtime"); got != 1 {
		t.Errorf("0 has to mean forever, not immediately: %d", got)
	}

	// Und dieselbe Bedeutung am Agenten, unter einer Organisation mit Frist.
	if err := s.obs.SetOrgRecordingRetention(ctx, s.orgID, 30); err != nil {
		t.Fatal(err)
	}
	null := 0
	if err := s.registry.SetRecordingRetention(ctx, agent.ID, &null); err != nil {
		t.Fatal(err)
	}
	if _, err := s.obs.CleanupRecordings(ctx); err != nil {
		t.Fatal(err)
	}
	if got := zaehle(t, s, agent.ID, "runtime"); got != 1 {
		t.Errorf("0 at the agent has to mean forever as well: %d", got)
	}
}
