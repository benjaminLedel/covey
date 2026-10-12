package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

const decisionWorkflow = `
workflow: Release
owner: admin
mode: steer
roles:
  Eng: [builder]
steps:
  t:
    type: trigger
    next: b
  b:
    type: agent
    title: Build
    role: Eng
    task: 'Build {{case.title}} [mock:action covey/report {"mr":"!9"}]'
    result: [mr]
    next: h
  h:
    type: approval
    title: Ship it?
    who: decider
    ask: 'Ship {{b.mr}}?'
    next:
      ship: w
      stop: e
  w:
    type: wait
    title: Deployed
    until: deployed
    next: e
  e:
    type: end
`

// TestWorkflowsThroughTheAPI walks the workflow routes (#564) as the
// interface will: a broken definition is refused with its problems, a good
// one saved and listed, a run started by hand, the approval it reaches
// decided by the person it names — and by nobody else — and the wait it
// then stands at ended by an event.
func TestWorkflowsThroughTheAPI(t *testing.T) {
	s := newStack(t)
	s.newSupportAgent("builder")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	s.mitglied(t, "decider@test.local", "Decider", "auditor", "decider-passwort")
	s.mitglied(t, "other@test.local", "Other", "auditor", "other-passwort")
	decider := login(t, s, "decider@test.local", "decider-passwort")
	other := login(t, s, "other@test.local", "other-passwort")

	// The editor's check, and a save of something that cannot run.
	checked := admin.expect(http.MethodPost, "/api/v1/workflows/check", map[string]string{"source": "steps:\n  e:\n    type: end\n"}, http.StatusOK)
	if ps := checked["problems"].([]any); len(ps) == 0 || ps[0].(map[string]any)["code"] != "no-trigger" {
		t.Fatalf("check: %v", checked)
	}
	refused := admin.expect(http.MethodPut, "/api/v1/workflows/release", map[string]string{"source": "steps:\n  e:\n    type: end\n"}, http.StatusUnprocessableEntity)
	if len(refused["problems"].([]any)) == 0 {
		t.Fatalf("refused without problems: %v", refused)
	}
	other.expect(http.MethodPut, "/api/v1/workflows/release", map[string]string{"source": decisionWorkflow}, http.StatusForbidden)

	saved := admin.expect(http.MethodPut, "/api/v1/workflows/release", map[string]string{"source": decisionWorkflow}, http.StatusOK)
	if saved["version"].(float64) != 1 || saved["warning"] != nil {
		t.Fatalf("save: %v", saved)
	}
	list := other.expectList(http.MethodGet, "/api/v1/workflows", nil, http.StatusOK)
	if len(list) != 1 || list[0]["slug"] != "release" || list[0]["name"] != "Release" {
		t.Fatalf("list: %v", list)
	}
	one := other.expect(http.MethodGet, "/api/v1/workflows/release", nil, http.StatusOK)
	if steps := one["steps"].([]any); len(steps) != 5 || !strings.Contains(one["source"].(string), "Ship it?") {
		t.Fatalf("one: %v", one)
	}

	// A run by hand; the same case again gets the same run.
	other.expect(http.MethodPost, "/api/v1/workflows/release/runs", map[string]any{"case": "v1.2"}, http.StatusForbidden)
	run := admin.expect(http.MethodPost, "/api/v1/workflows/release/runs", map[string]any{"case": "v1.2", "vars": map[string]string{"title": "1.2"}}, http.StatusCreated)
	id := run["id"].(string)
	again := admin.expect(http.MethodPost, "/api/v1/workflows/release/runs", map[string]any{"case": "v1.2"}, http.StatusOK)
	if again["id"] != id {
		t.Fatalf("a second run for the same case: %v", again)
	}

	path := "/api/v1/workflows/release/runs/" + id
	waitForOr(t, "the run to wait for the decision", 30*time.Second, func() bool {
		return admin.expect(http.MethodGet, path, nil, http.StatusOK)["step"] == "h"
	}, func() string { return admin.expect(http.MethodGet, path, nil, http.StatusOK)["state"].(string) })
	open := other.expectList(http.MethodGet, "/api/v1/workflows/release/runs?open=1", nil, http.StatusOK)
	if len(open) != 1 || open[0]["state"] != "waiting" {
		t.Fatalf("open runs: %v", open)
	}

	other.expect(http.MethodPost, path+"/decide", map[string]string{"option": "ship"}, http.StatusForbidden)
	decider.expect(http.MethodPost, path+"/decide", map[string]string{"option": "maybe"}, http.StatusBadRequest)
	decided := decider.expect(http.MethodPost, path+"/decide", map[string]string{"option": "ship"}, http.StatusOK)
	if decided["step"] != "w" || decided["state"] != "waiting" {
		t.Fatalf("after the decision: %v", decided)
	}
	decider.expect(http.MethodPost, path+"/decide", map[string]string{"option": "ship"}, http.StatusConflict)

	admin.expect(http.MethodPost, path+"/events", map[string]string{"name": "something"}, http.StatusConflict)
	done := admin.expect(http.MethodPost, path+"/events", map[string]string{"name": "deployed"}, http.StatusOK)
	if done["state"] != "done" || done["vars"].(map[string]any)["h"].(map[string]any)["decision"] != "ship" {
		t.Fatalf("after the event: %v", done)
	}
	if len(done["log"].([]any)) == 0 {
		t.Error("the run's log is not in its view")
	}
	admin.expect(http.MethodGet, "/api/v1/workflows/nope", nil, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/workflows/release/runs/nope", nil, http.StatusNotFound)
}
