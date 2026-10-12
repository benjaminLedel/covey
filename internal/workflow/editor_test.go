package workflow

import (
	"os"
	"testing"
	"time"
)

// The interface writes definitions with its own serializer (web/src/pages/
// workflows/model.ts, toSource). This file is its output for the example
// workflow, kept in the repository; the interface's test fails when the
// serializer writes something else, and this one when the engine stops
// reading it. Together they keep the two from drifting apart (#565).
func TestTheEngineReadsWhatTheEditorWrites(t *testing.T) {
	src, err := os.ReadFile("testdata/from-editor.workflow")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if ps := Check(d); len(ps) > 0 {
		t.Fatalf("the editor's example has problems: %v", ps)
	}
	b, c, w, h := d.Steps["b"], d.Steps["c"], d.Steps["w"], d.Steps["h"]
	if b.Limit != 4*time.Hour || b.MaxParallel != 2 || !b.Sticky || b.Pick != PickLoad || len(b.Result) != 1 {
		t.Errorf("build step: %+v", b)
	}
	if c.On != "a.result" || c.MaxRounds != 3 || c.Next["defects"] != "b" || c.Next[ExitLimit] != "h" {
		t.Errorf("condition: %+v", c)
	}
	if w.Max != 14*24*time.Hour || w.Next[ExitTimeout] != "e" {
		t.Errorf("wait: %+v", w)
	}
	if len(h.Options) != 2 || h.Options[0] != "retry" {
		t.Errorf("approval options: %v", h.Options)
	}
	if d.Mode != ModeWatch {
		t.Errorf("mode %q", d.Mode)
	}
}

// The same for a trigger with its sources and an action with parameters.
func TestTheEngineReadsTheEditorsTriggersAndActions(t *testing.T) {
	src, err := os.ReadFile("testdata/from-editor-triggers.workflow")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if ps := Check(d); len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	tr, s := d.Steps["t"], d.Steps["s"]
	if tr.As != "Engineering" || tr.Every != 15*time.Minute || tr.OnlyIf != "gitlab:issues:assignee:builder:acme/app" || tr.Webhook != "zammad" || !tr.Token {
		t.Errorf("trigger: %+v", tr)
	}
	if s.As != "Test" || s.System != "zammad" || s.Do != "set_state" || s.Params["ticket_id"] != "{{case.no}}" || s.Params["state"] != "closed" {
		t.Errorf("action: %+v", s)
	}
}
