package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

// covey/report takes a flat object: what a workflow's condition compares is a
// value, so text, numbers and true/false are taken as written and anything
// nested is refused rather than flattened into something nobody wrote.
func TestReportFieldsAreFlatValues(t *testing.T) {
	got, problem := reportFields(json.RawMessage(`{"result":"accepted","rounds":3,"ok":true,"mr":"!7"}`))
	if problem != "" {
		t.Fatal(problem)
	}
	want := map[string]string{"result": "accepted", "rounds": "3", "ok": "true", "mr": "!7"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	for name, in := range map[string]string{
		"not an object": `"accepted"`,
		"empty":         `{}`,
		"nested":        `{"result":{"a":1}}`,
		"list":          `{"defects":["a","b"]}`,
		"null":          `{"result":null}`,
		"empty name":    `{"":"x"}`,
		"too long":      `{"x":"` + strings.Repeat("a", 8001) + `"}`,
	} {
		if _, problem := reportFields(json.RawMessage(in)); problem == "" {
			t.Errorf("%s: accepted", name)
		}
	}
	many := map[string]string{}
	for i := 0; i < 33; i++ {
		many[strings.Repeat("k", i+1)] = "v"
	}
	raw, _ := json.Marshal(many)
	if _, problem := reportFields(raw); problem == "" {
		t.Error("33 fields accepted")
	}
}
