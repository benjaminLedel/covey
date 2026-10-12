package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/workflow"
)

// Workflows start from the doors agents already have and act through the
// actions agents already use (#566). These tests go through those doors.

func saveWorkflowAPI(t *testing.T, c *apiClient, slug, src string) map[string]any {
	t.Helper()
	return c.expect(http.MethodPut, "/api/v1/workflows/"+slug, map[string]string{"source": src}, http.StatusOK)
}

func postJSON(t *testing.T, url string, body []byte, header map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

const webhookWorkflow = `
workflow: Support
owner: admin
mode: steer
steps:
  t:
    type: trigger
    webhook: zammad
    next: w
  w:
    type: wait
    title: Customer answers
    until: "webhook:zammad"
    next: e
  e:
    type: end
`

// TestAZammadWebhookStartsAWorkflowAndAnswersItsWait: the same Zammad
// webhook an agent takes, verified and parsed by the plugin, starts a run for
// the ticket; the next event for the same ticket is delivered to the run and
// ends its wait; the same delivery twice is taken once.
func TestAZammadWebhookStartsAWorkflowAndAnswersItsWait(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	saveWorkflowAPI(t, admin, "support", webhookWorkflow)
	id := admin.expect(http.MethodGet, "/api/v1/workflows/support", nil, http.StatusOK)["id"].(string)
	url := s.http.URL + "/api/webhooks/zammad/workflows/" + id

	first := []byte(`{"ticket":{"id":42,"number":"20042","title":"Login fails","state":"open"},"article":{"id":1,"sender":"Customer","body":"I cannot log in.","internal":false}}`)
	code, out := postJSON(t, url, first, map[string]string{"X-Hub-Signature": signWebhook(first)})
	if code != http.StatusOK || out["outcome"] != "started" {
		t.Fatalf("first webhook: %d %v", code, out)
	}
	if code, out = postJSON(t, url, first, map[string]string{"X-Hub-Signature": signWebhook(first)}); out["outcome"] != "duplicate" {
		t.Fatalf("the same delivery again: %d %v", code, out)
	}
	if code, _ := postJSON(t, url, first, map[string]string{"X-Hub-Signature": "sha1=00"}); code != http.StatusUnauthorized {
		t.Fatalf("a wrong signature was taken: %d", code)
	}
	if code, _ := postJSON(t, s.http.URL+"/api/webhooks/jira/workflows/"+id, first, map[string]string{"X-Hub-Signature": signWebhook(first)}); code == http.StatusOK {
		t.Fatal("a workflow took a webhook from a system it does not name")
	}

	runs := admin.expectList(http.MethodGet, "/api/v1/workflows/support/runs", nil, http.StatusOK)
	if len(runs) != 1 || runs[0]["case"] != "zammad:ticket:42" || runs[0]["step"] != "w" || runs[0]["state"] != "waiting" {
		t.Fatalf("runs: %v", runs)
	}

	reply := []byte(`{"ticket":{"id":42,"number":"20042","title":"Login fails","state":"open"},"article":{"id":2,"sender":"Customer","body":"Firefox.","internal":false}}`)
	if code, out = postJSON(t, url, reply, map[string]string{"X-Hub-Signature": signWebhook(reply)}); out["outcome"] != "delivered" {
		t.Fatalf("the answer: %d %v", code, out)
	}
	done := admin.expect(http.MethodGet, "/api/v1/workflows/support/runs/"+runs[0]["id"].(string), nil, http.StatusOK)
	if done["state"] != "done" {
		t.Fatalf("the wait did not end: %v", done)
	}
}

const tokenWorkflow = `
workflow: Orders
owner: admin
mode: steer
roles:
  Support: [support]
steps:
  t:
    type: trigger
    token: true
    next: s
  s:
    type: action
    title: Close the ticket
    as: Support
    system: zammad
    do: set_state
    params:
      ticket_id: "{{case.no}}"
      state: closed
    next: e
  e:
    type: end
`

// TestATokenTriggerStartsARunWhoseActionRunsAsAColleague: the token trigger
// agents have starts a run; its action runs as the support agent, through
// that agent's credential, and reaches the target system. Under a rule that
// requires approval the run waits for the approval and goes on once it is
// granted.
func TestATokenTriggerStartsARunWhoseActionRunsAsAColleague(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	zammad := newFakeZammad(t)
	agent := s.newSupportAgent("support")
	for k, v := range map[string]string{"zammad_token": "tok", "zammad_url": zammad.srv.URL} {
		if err := s.secrets.Put(ctx, s.orgID, k, v); err != nil {
			t.Fatal(err)
		}
		if err := s.secrets.Assign(ctx, s.orgID, k, agent.ID); err != nil {
			t.Fatal(err)
		}
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	saveWorkflowAPI(t, admin, "orders", tokenWorkflow)
	tok := admin.expect(http.MethodPost, "/api/v1/workflows/orders/token", nil, http.StatusOK)
	url := tok["url"].(string)
	if !strings.Contains(url, "/api/trigger/workflows/") {
		t.Fatalf("token url %q", url)
	}

	// The case is the ticket: it reaches the action as {{case.no}}.
	code, out := postJSON(t, url, []byte(`{"case":"42","title":"Close 42","dedup_key":"order-42"}`), nil)
	if code != http.StatusOK || out["outcome"] != "started" {
		t.Fatalf("trigger: %d %v", code, out)
	}
	if _, out = postJSON(t, url, []byte(`{"case":"42","dedup_key":"order-42"}`), nil); out["outcome"] != "duplicate" {
		t.Fatalf("the same delivery again: %v", out)
	}
	if code, _ := postJSON(t, s.http.URL+"/api/trigger/workflows/nope", []byte(`{}`), nil); code != http.StatusNotFound {
		t.Fatalf("an unknown token: %d", code)
	}

	runs := admin.expectList(http.MethodGet, "/api/v1/workflows/orders/runs", nil, http.StatusOK)
	if len(runs) != 1 || runs[0]["case"] != "42" || runs[0]["state"] != "done" {
		t.Fatalf("the action did not run: %v", runs)
	}
	if upd := zammad.lastUpdate(); upd == nil || upd["state"] != "closed" {
		t.Fatalf("zammad did not get the action: %v", upd)
	}

	// Under a rule that requires approval the run waits for it.
	if _, err := s.rails.Create(ctx, railRule(s.orgID, "require_approval", "zammad:set_state")); err != nil {
		t.Fatal(err)
	}
	waiting := admin.expect(http.MethodPost, "/api/v1/workflows/orders/runs", map[string]any{"case": "44", "vars": map[string]string{"no": "44"}}, http.StatusCreated)
	if waiting["state"] != "waiting" || waiting["step"] != "s" {
		t.Fatalf("the run did not wait for the approval: %v", waiting)
	}
	approval := waiting["vars"].(map[string]any)["s"].(map[string]any)["approval"].(string)
	appr, err := s.obs.GetApproval(ctx, s.orgID, mustUUID(t, approval))
	if err != nil || appr.Status != "pending" || appr.AgentID != agent.ID || appr.TaskID != nil {
		t.Fatalf("approval %+v, %v", appr, err)
	}
	s.workflows.Tick()
	if r := admin.expect(http.MethodGet, "/api/v1/workflows/orders/runs/"+waiting["id"].(string), nil, http.StatusOK); r["state"] != "waiting" {
		t.Fatalf("went on without the approval: %v", r)
	}
	if _, err := s.obs.DecideApproval(ctx, s.orgID, appr.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	s.workflows.Tick()
	if r := admin.expect(http.MethodGet, "/api/v1/workflows/orders/runs/"+waiting["id"].(string), nil, http.StatusOK); r["state"] != "done" {
		t.Fatalf("did not go on after the approval: %v", r)
	}
	if upd := zammad.lastUpdate(); upd["state"] != "closed" {
		t.Fatalf("zammad: %v", upd)
	}
}

const scheduleWorkflow = `
workflow: Weekly
owner: admin
mode: steer
steps:
  t:
    type: trigger
    title: Weekly report
    as: support
    every: 1h
    only_if: zammad
    next: w
  w:
    type: wait
    until: never
    next: e
  e:
    type: end
`

// TestAScheduleFiresLikeAHeartbeat: due by `every`, through the heartbeat's
// gate under the colleague's access, once per period.
func TestAScheduleFiresLikeAHeartbeat(t *testing.T) {
	s := newStack(t)
	s.newSupportAgent("support")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local)
	s.workflows.Now = func() time.Time { return now }
	saveWorkflowAPI(t, admin, "weekly", scheduleWorkflow)

	s.workflows.Tick()
	runs := admin.expectList(http.MethodGet, "/api/v1/workflows/weekly/runs", nil, http.StatusOK)
	if len(runs) != 1 || !strings.HasPrefix(runs[0]["case"].(string), "schedule:") || runs[0]["title"] != "Weekly report · 2026-10-12 09:00" {
		t.Fatalf("first firing: %v", runs)
	}
	s.workflows.Tick()
	if n := len(admin.expectList(http.MethodGet, "/api/v1/workflows/weekly/runs", nil, http.StatusOK)); n != 1 {
		t.Fatalf("fired again before it was due: %d runs", n)
	}
	st, _ := workflow.NewStore(s.pool).Get(context.Background(), s.orgID, "weekly")
	state, _ := workflow.NewStore(s.pool).Trigger(context.Background(), st.ID)
	if state.LastFired == nil || !state.LastFired.Equal(now) {
		t.Fatalf("trigger state: %+v", state)
	}
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
