package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"covey/internal/workflow"
	"covey/internal/workflowhost"
)

// hostWorkflows takes up what a test saved straight into the store — the
// API does the same after a PUT.
func hostWorkflows(t *testing.T, s *stack, slug string) *workflowhost.Host {
	t.Helper()
	if err := s.workflows.Reload(context.Background(), s.orgID, slug); err != nil {
		t.Fatal(err)
	}
	return s.workflows
}

// The steps' tasks carry the mock runtime's directives: the agent reports
// with covey/report exactly as a real one would through its action proxy.
const handOverWorkflow = `
owner: alex
mode: steer
roles:
  Eng: [builder]
  Test: [tester]
steps:
  t:
    type: trigger
    next: b
  b:
    type: agent
    title: Build
    role: Eng
    task: 'Build {{case.title}} [mock:action covey/report {"mr":"!7"}]'
    result: [mr]
    next: a
  a:
    type: agent
    title: Acceptance
    role: Test
    not_same_as: b
    task: 'Test {{b.mr}} [mock:action covey/report {"result":"%s"}]'
    result: [result]
    next: c
  c:
    type: condition
    on: a.result
    next:
      accepted: e
      else: e
  e:
    type: end
`

// TestAWorkflowHandsItsStepsToAgentsAndMovesOnWithTheirReports runs a case
// through two agents end to end (#563): the host gives the build step to
// the builder as a task, the builder's run reports the merge request with
// covey/report, the task ends, the workflow gives acceptance to the tester
// with the merge request in its task, and the tester's report ends the run.
func TestAWorkflowHandsItsStepsToAgentsAndMovesOnWithTheirReports(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	builder := s.newSupportAgent("builder")
	tester := s.newSupportAgent("tester")
	st := workflow.NewStore(s.pool)
	if _, err := st.Save(ctx, s.orgID, "handover", strings.Replace(handOverWorkflow, "%s", "accepted", 1), nil); err != nil {
		t.Fatal(err)
	}
	h := hostWorkflows(t, s, "handover")
	eng, _, ok := h.Engine(s.orgID, "handover")
	if !ok {
		t.Fatal("the saved workflow was not loaded")
	}

	r, err := eng.Start("#42", map[string]string{"title": "CSV export"})
	if err != nil {
		t.Fatal(err)
	}
	waitForOr(t, "the run to end", 30*time.Second, func() bool {
		cur, _ := eng.Run(r.ID)
		return cur.State == workflow.StateDone || cur.State == workflow.StateFailed
	}, func() string {
		cur, _ := eng.Run(r.ID)
		return string(cur.State) + " at " + cur.Step
	})

	done, _ := eng.Run(r.ID)
	if done.State != workflow.StateDone {
		t.Fatalf("run ended %s at %s", done.State, done.Step)
	}
	if done.Had["b"] != "builder" || done.Had["a"] != "tester" || done.Vars["b"]["mr"] != "!7" || done.Vars["a"]["result"] != "accepted" {
		t.Fatalf("run: had %v, vars %v", done.Had, done.Vars)
	}

	// Each step was a task in the right agent's backlog, its origin naming
	// the run and the step, its text rendered and asking for the report.
	bt, _ := s.backlog.ListByAgent(ctx, builder.ID, true)
	at, _ := s.backlog.ListByAgent(ctx, tester.ID, true)
	if len(bt) != 1 || len(at) != 1 {
		t.Fatalf("tasks: builder %d, tester %d", len(bt), len(at))
	}
	if !strings.HasPrefix(bt[0].Origin, workflowhost.OriginPrefix) || !strings.HasSuffix(bt[0].Origin, ":"+r.ID+":b") {
		t.Errorf("builder task origin %q", bt[0].Origin)
	}
	if bt[0].Title != "Build · #42" || !strings.Contains(bt[0].Body, "Build CSV export") || !strings.Contains(bt[0].Body, "covey/report") {
		t.Errorf("builder task: %q / %q", bt[0].Title, bt[0].Body)
	}
	if !strings.Contains(at[0].Body, "Test !7") {
		t.Errorf("the tester's task does not carry the builder's report: %q", at[0].Body)
	}

	// What the database holds is what the engine holds.
	w, err := st.Get(ctx, s.orgID, "handover")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.Runs(ctx, w.ID)
	if err != nil || len(stored) != 1 || stored[0].State != workflow.StateDone || stored[0].Vars["a"]["result"] != "accepted" {
		t.Fatalf("stored runs: %+v, %v", stored, err)
	}
}

// TestAStepThatEndsWithoutItsReportStopsTheRun: an agent that finishes
// without the fields its step asks for leaves the workflow nothing to decide
// on. The run stops there and says why, instead of guessing.
func TestAStepThatEndsWithoutItsReportStopsTheRun(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.newSupportAgent("builder")
	s.newSupportAgent("tester")
	src := strings.Replace(handOverWorkflow, `'Test {{b.mr}} [mock:action covey/report {"result":"%s"}]'`, `'Test {{b.mr}}'`, 1)
	if _, err := workflow.NewStore(s.pool).Save(ctx, s.orgID, "handover", src, nil); err != nil {
		t.Fatal(err)
	}
	h := hostWorkflows(t, s, "handover")
	eng, _, _ := h.Engine(s.orgID, "handover")
	r, _ := eng.Start("#43", map[string]string{"title": "Import"})
	waitForOr(t, "the run to stop", 30*time.Second, func() bool {
		cur, _ := eng.Run(r.ID)
		return cur.State == workflow.StateFailed || cur.State == workflow.StateDone
	}, func() string {
		cur, _ := eng.Run(r.ID)
		return string(cur.State) + " at " + cur.Step
	})
	cur, _ := eng.Run(r.ID)
	if cur.State != workflow.StateFailed || cur.Step != "a" {
		t.Fatalf("run ended %s at %s", cur.State, cur.Step)
	}
	last := cur.Log[len(cur.Log)-1].Text
	if !strings.Contains(last, "without reporting") {
		t.Errorf("the log does not say why: %q", last)
	}
}
