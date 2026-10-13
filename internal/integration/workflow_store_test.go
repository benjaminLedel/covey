package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/workflow"
)

// quietWorld is the outside of a workflow engine that agrees to everything:
// these tests are about what is kept, not about what is sent.
type quietWorld struct{ away map[string]bool }

func (q quietWorld) Available(name string) bool  { return !q.away[name] }
func (quietWorld) Dispatch(workflow.Task) error  { return nil }
func (quietWorld) Act(workflow.Action) error     { return nil }
func (quietWorld) Ask(workflow.Question) error   { return nil }
func (quietWorld) Notify(workflow.Message) error { return nil }

func exampleWorkflow(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("../workflow/testdata/customer-ticket.workflow")
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

// TestWorkflowsAreKeptVersionedAndOnlyWhenTheyCanRun walks the store of
// workflow definitions (#561): a broken one is refused with its problems, a
// good one becomes version 1, a change version 2, the old one stays readable,
// and another organisation sees none of it.
func TestWorkflowsAreKeptVersionedAndOnlyWhenTheyCanRun(t *testing.T) {
	s := newStoreStack(t)
	ctx := context.Background()
	st := workflow.NewStore(s.pool)
	src := exampleWorkflow(t)

	var invalid *workflow.InvalidError
	if _, err := st.Save(ctx, s.orgID, "ticket", "steps:\n  e:\n    type: end\n", nil); !errors.As(err, &invalid) || invalid.Problems[0].Code != "no-trigger" {
		t.Fatalf("a workflow without a trigger was not refused with its problem: %v", err)
	}
	if _, err := st.Save(ctx, s.orgID, "Kunden Ticket", src, nil); !errors.As(err, &invalid) {
		t.Fatalf("a slug with spaces was kept: %v", err)
	}
	if _, err := st.Get(ctx, s.orgID, "ticket"); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("a refused save left something behind: %v", err)
	}

	v1, err := st.Save(ctx, s.orgID, "ticket", src, nil)
	if err != nil || v1.Version != 1 {
		t.Fatalf("first save: %+v, %v", v1, err)
	}
	changed := strings.Replace(src, "max_rounds: 3", "max_rounds: 5", 1)
	v2, err := st.Save(ctx, s.orgID, "ticket", changed, nil)
	if err != nil || v2.Version != 2 || v2.ID != v1.ID {
		t.Fatalf("second save: %+v, %v", v2, err)
	}
	cur, err := st.Get(ctx, s.orgID, "ticket")
	if err != nil || cur.Version != 2 || cur.Def.Steps["c"].MaxRounds != 5 {
		t.Fatalf("current: %+v, %v", cur, err)
	}
	old, err := st.Version(ctx, s.orgID, "ticket", 1)
	if err != nil || old.Def.Steps["c"].MaxRounds != 3 {
		t.Fatalf("version 1 is not what was saved: %+v, %v", old, err)
	}
	list, err := st.List(ctx, s.orgID)
	if err != nil || len(list) != 1 || list[0].Version != 2 {
		t.Fatalf("list: %+v, %v", list, err)
	}

	stranger := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1, 'Nachbar')", stranger); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, stranger, "ticket"); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("another organisation reads the workflow: %v", err)
	}
	if l, _ := st.List(ctx, stranger); len(l) != 0 {
		t.Fatalf("another organisation lists %d workflows", len(l))
	}
}

// TestWorkflowRunsSurviveARestart: an engine whose runs are stored as they
// change can be rebuilt from the database and carries on where it stood —
// and the database itself refuses a second open run for the same case.
func TestWorkflowRunsSurviveARestart(t *testing.T) {
	s := newStoreStack(t)
	ctx := context.Background()
	st := workflow.NewStore(s.pool)
	saved, err := st.Save(ctx, s.orgID, "ticket", exampleWorkflow(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	outside := quietWorld{away: map[string]bool{"theo": true}}
	keep := func(e *workflow.Engine) {
		e.OnChange(func(rs []workflow.Run) {
			if err := st.SaveRuns(ctx, saved.ID, rs); err != nil {
				t.Errorf("save runs: %v", err)
			}
		})
	}

	before, err := workflow.New(saved.Def, outside, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	keep(before)
	r, _ := before.Start("#384", map[string]string{"no": "#384", "title": "Import with umlauts"})
	if err := before.Done(r.ID, map[string]string{"mr": "!198"}); err != nil {
		t.Fatal(err)
	}
	if err := before.Done(r.ID, map[string]string{"result": "defects", "defects": "ß becomes ss"}); err != nil {
		t.Fatal(err)
	}
	before.Start("#385", map[string]string{"no": "#385"})

	// Restart: read back, rebuild, continue.
	stored, err := st.Runs(ctx, saved.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("runs read back: %d, %v", len(stored), err)
	}
	got := stored[0]
	if got.ID != r.ID || got.Step != "b" || got.Assignee != "ada" || got.Loops["c"] != 1 || got.Had["a"] != "rosa" || got.Vars["a"]["defects"] != "ß becomes ss" {
		t.Fatalf("the run did not come back as it was: %+v", got)
	}
	if len(got.Log) == 0 || got.Log[0].Text == "" {
		t.Fatal("the log was not kept")
	}

	after, err := workflow.New(saved.Def, outside, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	keep(after)
	if err := after.Restore(stored); err != nil {
		t.Fatal(err)
	}
	if err := after.Done(r.ID, map[string]string{"mr": "!199"}); err != nil {
		t.Fatal(err)
	}
	again, _ := st.Runs(ctx, saved.ID)
	if again[0].Step != "a" || again[0].Vars["b"]["mr"] != "!199" {
		t.Fatalf("the restored engine's step was not stored: %+v", again[0])
	}

	// A second open run for #384, under another id, is refused by the table.
	dup := again[0]
	dup.ID = uuid.NewString()
	if err := st.SaveRuns(ctx, saved.ID, []workflow.Run{dup}); !errors.Is(err, workflow.ErrCaseOpen) {
		t.Fatalf("a second open run for one case was stored: %v", err)
	}
}
