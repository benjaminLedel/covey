package daemon

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParseStatusLine(t *testing.T) {
	out := `Ich habe das Ticket gelesen und eine Rückfrage gestellt.

COVEY_STATUS: {"status":"blocked","correlation_key":"zammad:ticket:42","question":"Warte auf Screenshot"}`
	st, ok := ParseStatusLine(out)
	if !ok || st.Status != "blocked" || st.CorrelationKey != "zammad:ticket:42" {
		t.Fatalf("blocked status not parsed: %+v ok=%v", st, ok)
	}
}

func TestParseStatusLineLastWins(t *testing.T) {
	out := `COVEY_STATUS: {"status":"blocked","correlation_key":"a"}
Weitere Arbeit …
COVEY_STATUS: {"status":"done","result":"fertig","memory":"gelernt"}`
	st, ok := ParseStatusLine(out)
	if !ok || st.Status != "done" || st.Memory != "gelernt" {
		t.Fatalf("the last status line must win: %+v", st)
	}
}

func TestParseStatusLineMissing(t *testing.T) {
	if _, ok := ParseStatusLine("just text without a marker"); ok {
		t.Fatal("without a marker nothing may be parsed")
	}
	if _, ok := ParseStatusLine("COVEY_STATUS: no json"); ok {
		t.Fatal("broken JSON must not be ok")
	}
}

func TestApplyStatusBlockedWithoutKeyFails(t *testing.T) {
	var res RunResult
	applyStatus(&res, `COVEY_STATUS: {"status":"blocked","question":"?"}`)
	if res.Status != "failed" {
		t.Fatalf("blocked without a correlation_key must never park (unwakeable): %+v", res)
	}
}

func TestApplyStatusDefaultDone(t *testing.T) {
	var res RunResult
	applyStatus(&res, "just a plain answer")
	if res.Status != "done" || res.Result != "just a plain answer" {
		t.Fatalf("without a marker: done with the whole text, got %+v", res)
	}
}

func TestMockRuntimeBlockAndResume(t *testing.T) {
	m := Mock{}
	events := 0
	spec := RunSpec{
		TaskID: "t1", Title: "Ticket 42",
		Body: "Bitte klären.\n[mock:block key=zammad:ticket:42 question=Warte auf Kunde]",
	}
	res, err := m.Run(context.Background(), spec, func(string, json.RawMessage) { events++ })
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "blocked" || res.CorrelationKey != "zammad:ticket:42" {
		t.Fatalf("mock must block: %+v", res)
	}
	if res.SessionID == "" {
		t.Fatal("session_id must be set (for --resume)")
	}
	if events == 0 {
		t.Fatal("mock must emit runtime events (recording)")
	}

	// Resumption: the same directive must not block again.
	spec.ResumeSessionID = res.SessionID
	spec.ResumeInput = "Kunde hat geantwortet: Chrome 126"
	res2, err := m.Run(context.Background(), spec, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != "done" {
		t.Fatalf("resume must deliver done: %+v", res2)
	}
	if res2.SessionID != res.SessionID {
		t.Fatal("resume must keep the session")
	}
}

func TestMockRuntimeDirectives(t *testing.T) {
	m := Mock{}
	res, _ := m.Run(context.Background(), RunSpec{
		TaskID: "t2", Title: "X",
		Body: "[mock:result Alles erledigt]\n[mock:memory Kunde nutzt Chrome]",
	}, func(string, json.RawMessage) {})
	if res.Status != "done" || res.Result != "Alles erledigt" || res.Memory != "Kunde nutzt Chrome" {
		t.Fatalf("directives wrong: %+v", res)
	}

	res, _ = m.Run(context.Background(), RunSpec{TaskID: "t3", Title: "X",
		Body: "[mock:fail kaputt]"}, func(string, json.RawMessage) {})
	if res.Status != "failed" || res.Error != "kaputt" {
		t.Fatalf("fail directive wrong: %+v", res)
	}
}

// BuiltinTools separates the two flags: built-in names go into --tools, MCP
// names do not — the flag would drop them silently while the tool disappears
// from the run.
func TestBuiltinToolsDropsMCPNames(t *testing.T) {
	got := BuiltinTools([]string{"Bash", "mcp__covey", "Read"}, false)
	want := []string{"Bash", "Read"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// With skills the Skill tool joins them — but only once, even if the scope
// already names it.
func TestBuiltinToolsAddsSkillOnlyOnce(t *testing.T) {
	if got := BuiltinTools([]string{"Bash"}, true); len(got) != 2 || got[1] != "Skill" {
		t.Fatalf("Skill should be appended: %v", got)
	}
	if got := BuiltinTools([]string{"Bash", "Skill"}, true); len(got) != 2 {
		t.Fatalf("Skill must not be doubled: %v", got)
	}
}

// A scope of nothing but MCP names yields nil so the caller leaves the flag
// off: an EMPTY --tools list means "no tools at all" to the runtime, which
// would silently strip the run of its shell.
func TestBuiltinToolsEmptyStaysNil(t *testing.T) {
	if got := BuiltinTools([]string{"mcp__covey"}, false); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
