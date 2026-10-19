package integration

import (
	"context"
	"fmt"
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

	mine := decider.expectList(http.MethodGet, "/api/v1/workflows/mine", nil, http.StatusOK)
	if len(mine) != 1 || mine[0]["question"] != "Ship !9?" || mine[0]["run"] != id || mine[0]["title"] != "1.2" {
		t.Fatalf("approvals for the decider: %v", mine)
	}
	if n := len(other.expectList(http.MethodGet, "/api/v1/workflows/mine", nil, http.StatusOK)); n != 0 {
		t.Fatalf("someone the approval does not name sees %d", n)
	}
	admin.expect(http.MethodPut, "/api/v1/workflows/mine", map[string]string{"source": decisionWorkflow}, http.StatusUnprocessableEntity)
	steps := one["steps"].([]any)
	if b := steps[1].(map[string]any); b["task"] == nil || b["result"] == nil || b["role"] != "Eng" {
		t.Errorf("a step's view lacks its fields: %v", b)
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

const deletableWorkflow = `
owner: admin
mode: %s
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
    task: '%s'
    result: [mr]
    next: e
  e:
    type: end
`

// TestAWorkflowIsDeletedOnlyWhenItIsOffAndNobodyWorksOnIt (#577): a
// workflow that still watches is refused, so is one whose colleague still
// has a step; one that is off goes with its runs, and is gone from the list.
func TestAWorkflowIsDeletedOnlyWhenItIsOffAndNobodyWorksOnIt(t *testing.T) {
	s := newStack(t)
	s.newSupportAgent("builder")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	s.mitglied(t, "other@test.local", "Other", "auditor", "other-passwort")
	other := login(t, s, "other@test.local", "other-passwort")
	put := func(slug, mode, task string) {
		admin.expect(http.MethodPut, "/api/v1/workflows/"+slug, map[string]string{"source": fmt.Sprintf(deletableWorkflow, mode, task)}, http.StatusOK)
	}

	// Watched runs go with the workflow, once it is off.
	put("watched", "watch", "Build")
	admin.expect(http.MethodPost, "/api/v1/workflows/watched/runs", map[string]any{"case": "c1"}, http.StatusCreated)
	other.expect(http.MethodDelete, "/api/v1/workflows/watched", nil, http.StatusForbidden)
	admin.expect(http.MethodDelete, "/api/v1/workflows/watched", nil, http.StatusConflict)
	put("watched", "off", "Build")
	admin.expect(http.MethodDelete, "/api/v1/workflows/watched", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/workflows/watched", nil, http.StatusNotFound)
	admin.expect(http.MethodDelete, "/api/v1/workflows/watched", nil, http.StatusNotFound)

	// A colleague at work keeps the workflow, even switched off.
	put("busy", "steer", "Build [mock:sleep 30s]")
	run := admin.expect(http.MethodPost, "/api/v1/workflows/busy/runs", map[string]any{"case": "c2"}, http.StatusCreated)
	path := "/api/v1/workflows/busy/runs/" + run["id"].(string)
	waitForOr(t, "the build step to be worked on", 30*time.Second, func() bool {
		return admin.expect(http.MethodGet, path, nil, http.StatusOK)["state"] == "working"
	}, func() string { return admin.expect(http.MethodGet, path, nil, http.StatusOK)["state"].(string) })
	put("busy", "off", "Build [mock:sleep 30s]")
	admin.expect(http.MethodDelete, "/api/v1/workflows/busy", nil, http.StatusConflict)

	list := admin.expectList(http.MethodGet, "/api/v1/workflows", nil, http.StatusOK)
	if len(list) != 1 || list[0]["slug"] != "busy" {
		t.Fatalf("list after deleting: %v", list)
	}
}

// TestARunIsCancelledByHandAndItsStepsTaskWithIt (#598): a run is ended by
// hand by whoever may start one, with a reason in its log; the task its
// colleague was at work on is cancelled with it; the case is free again.
func TestARunIsCancelledByHandAndItsStepsTaskWithIt(t *testing.T) {
	s := newStack(t)
	builder := s.newSupportAgent("builder")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	s.mitglied(t, "other@test.local", "Other", "auditor", "other-passwort")
	other := login(t, s, "other@test.local", "other-passwort")
	admin.expect(http.MethodPut, "/api/v1/workflows/busy", map[string]string{"source": fmt.Sprintf(deletableWorkflow, "steer", "Build [mock:sleep 30s]")}, http.StatusOK)

	run := admin.expect(http.MethodPost, "/api/v1/workflows/busy/runs", map[string]any{"case": "c1"}, http.StatusCreated)
	path := "/api/v1/workflows/busy/runs/" + run["id"].(string)
	waitForOr(t, "the build step to be worked on", 30*time.Second, func() bool {
		return admin.expect(http.MethodGet, path, nil, http.StatusOK)["state"] == "working"
	}, func() string { return admin.expect(http.MethodGet, path, nil, http.StatusOK)["state"].(string) })

	other.expect(http.MethodPost, path+"/cancel", map[string]string{"reason": "no"}, http.StatusForbidden)
	ended := admin.expect(http.MethodPost, path+"/cancel", map[string]string{"reason": "wrong case"}, http.StatusOK)
	if ended["state"] != "failed" || ended["step"] != "b" {
		t.Fatalf("after cancelling: %v", ended)
	}
	log := ended["log"].([]any)
	if last := log[len(log)-1].(map[string]any)["text"]; last != "cancelled by admin@test.local: wrong case" {
		t.Fatalf("log: %v", last)
	}
	tasks, err := s.backlog.ListByAgent(context.Background(), builder.ID, true)
	if err != nil || len(tasks) != 1 || tasks[0].State != "cancelled" {
		t.Fatalf("the step's task: %+v %v", tasks, err)
	}

	admin.expect(http.MethodPost, path+"/cancel", nil, http.StatusConflict)
	admin.expect(http.MethodPost, "/api/v1/workflows/busy/runs/nope/cancel", nil, http.StatusNotFound)
	again := admin.expect(http.MethodPost, "/api/v1/workflows/busy/runs", map[string]any{"case": "c1"}, http.StatusCreated)
	if again["id"] == run["id"] {
		t.Fatal("the cancelled run was continued")
	}
}

// TestAWorkflowIsCreatedAsANewOneAndAddressedByItsID (#580): creating under
// a name that is taken makes a second workflow rather than a version of the
// first, and every route takes the id as well as the slug.
func TestAWorkflowIsCreatedAsANewOneAndAddressedByItsID(t *testing.T) {
	s := newStack(t)
	s.newSupportAgent("builder")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	s.mitglied(t, "decider@test.local", "Decider", "auditor", "decider-passwort")
	decider := login(t, s, "decider@test.local", "decider-passwort")
	decider.expect(http.MethodPost, "/api/v1/workflows", map[string]string{"slug": "release", "source": decisionWorkflow}, http.StatusForbidden)

	first := admin.expect(http.MethodPost, "/api/v1/workflows", map[string]string{"slug": "release", "source": decisionWorkflow}, http.StatusCreated)
	second := admin.expect(http.MethodPost, "/api/v1/workflows", map[string]string{"slug": "release", "source": decisionWorkflow}, http.StatusCreated)
	if first["slug"] != "release" || second["slug"] != "release-2" || first["id"] == second["id"] || second["version"].(float64) != 1 {
		t.Fatalf("created %v and %v", first, second)
	}
	if n := len(admin.expectList(http.MethodGet, "/api/v1/workflows", nil, http.StatusOK)); n != 2 {
		t.Fatalf("%d workflows after creating two", n)
	}

	id := second["id"].(string)
	one := admin.expect(http.MethodGet, "/api/v1/workflows/"+id, nil, http.StatusOK)
	if one["slug"] != "release-2" || one["id"] != id {
		t.Fatalf("by id: %v", one)
	}
	saved := admin.expect(http.MethodPut, "/api/v1/workflows/"+id, map[string]string{"source": decisionWorkflow}, http.StatusOK)
	if saved["slug"] != "release-2" || saved["version"].(float64) != 2 {
		t.Fatalf("saved by id: %v", saved)
	}
	admin.expect(http.MethodGet, "/api/v1/workflows/00000000-0000-0000-0000-000000000001", nil, http.StatusNotFound)
	admin.expect(http.MethodPut, "/api/v1/workflows/00000000-0000-0000-0000-000000000001", map[string]string{"source": decisionWorkflow}, http.StatusNotFound)

	run := admin.expect(http.MethodPost, "/api/v1/workflows/"+id+"/runs", map[string]any{"case": "v2"}, http.StatusCreated)
	path := "/api/v1/workflows/" + id + "/runs/" + run["id"].(string)
	waitForOr(t, "the run to wait for the decision", 30*time.Second, func() bool {
		return admin.expect(http.MethodGet, path, nil, http.StatusOK)["step"] == "h"
	}, func() string { return admin.expect(http.MethodGet, path, nil, http.StatusOK)["state"].(string) })
	mine := decider.expectList(http.MethodGet, "/api/v1/workflows/mine", nil, http.StatusOK)
	if len(mine) != 1 || mine[0]["workflow_id"] != id {
		t.Fatalf("the approval does not name the workflow's id: %v", mine)
	}
}
