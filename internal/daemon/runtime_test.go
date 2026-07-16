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
		t.Fatalf("blocked-Status nicht geparst: %+v ok=%v", st, ok)
	}
}

func TestParseStatusLineLastWins(t *testing.T) {
	out := `COVEY_STATUS: {"status":"blocked","correlation_key":"a"}
Weitere Arbeit …
COVEY_STATUS: {"status":"done","result":"fertig","memory":"gelernt"}`
	st, ok := ParseStatusLine(out)
	if !ok || st.Status != "done" || st.Memory != "gelernt" {
		t.Fatalf("letzte Status-Zeile muss gewinnen: %+v", st)
	}
}

func TestParseStatusLineMissing(t *testing.T) {
	if _, ok := ParseStatusLine("nur text ohne marker"); ok {
		t.Fatal("ohne Marker darf nichts geparst werden")
	}
	if _, ok := ParseStatusLine("COVEY_STATUS: kein json"); ok {
		t.Fatal("kaputtes JSON darf nicht ok sein")
	}
}

func TestApplyStatusBlockedWithoutKeyFails(t *testing.T) {
	var res RunResult
	applyStatus(&res, `COVEY_STATUS: {"status":"blocked","question":"?"}`)
	if res.Status != "failed" {
		t.Fatalf("blocked ohne correlation_key darf nie parken (unweckbar): %+v", res)
	}
}

func TestApplyStatusDefaultDone(t *testing.T) {
	var res RunResult
	applyStatus(&res, "einfach nur eine Antwort")
	if res.Status != "done" || res.Result != "einfach nur eine Antwort" {
		t.Fatalf("ohne Marker: done mit Gesamttext, got %+v", res)
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
		t.Fatalf("mock muss blocken: %+v", res)
	}
	if res.SessionID == "" {
		t.Fatal("session_id muss gesetzt sein (für --resume)")
	}
	if events == 0 {
		t.Fatal("mock muss runtime-events emittieren (Recording)")
	}

	// Wiederaufnahme: dieselbe Direktive darf nicht erneut blocken.
	spec.ResumeSessionID = res.SessionID
	spec.ResumeInput = "Kunde hat geantwortet: Chrome 126"
	res2, err := m.Run(context.Background(), spec, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != "done" {
		t.Fatalf("resume muss done liefern: %+v", res2)
	}
	if res2.SessionID != res.SessionID {
		t.Fatal("resume muss die session behalten")
	}
}

func TestMockRuntimeDirectives(t *testing.T) {
	m := Mock{}
	res, _ := m.Run(context.Background(), RunSpec{
		TaskID: "t2", Title: "X",
		Body: "[mock:result Alles erledigt]\n[mock:memory Kunde nutzt Chrome]",
	}, func(string, json.RawMessage) {})
	if res.Status != "done" || res.Result != "Alles erledigt" || res.Memory != "Kunde nutzt Chrome" {
		t.Fatalf("direktiven falsch: %+v", res)
	}

	res, _ = m.Run(context.Background(), RunSpec{TaskID: "t3", Title: "X",
		Body: "[mock:fail kaputt]"}, func(string, json.RawMessage) {})
	if res.Status != "failed" || res.Error != "kaputt" {
		t.Fatalf("fail-direktive falsch: %+v", res)
	}
}
