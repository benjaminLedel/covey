package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/guardrails"
	"covey/internal/style"
)

func TestFreeTextCollectsLongStringsOnly(t *testing.T) {
	long := strings.Repeat("Ein Satz mit einigen Wörtern darin. ", 12)
	params := json.RawMessage(`{"title":"Kurzer Titel","body":` + mustJSON(long) + `,"labels":["a","b"],"nested":{"note":` + mustJSON(long) + `}}`)
	got := freeText(params, 60)
	if strings.Count(got, "Ein Satz") != 24 || strings.Contains(got, "Kurzer Titel") {
		t.Fatalf("freeText: %q", got[:80])
	}
	if freeText(json.RawMessage(`"not an object"`), 60) != "" || freeText(nil, 60) != "" {
		t.Fatal("short or empty params have no free text")
	}
}

func TestStrictestStyleGate(t *testing.T) {
	a := uuid.New()
	gates := []guardrails.Rule{
		{ID: uuid.New(), Pattern: "*", Params: json.RawMessage(`{"mode":"warn","min_words":80}`)},
		{ID: uuid.New(), AgentID: &a, Pattern: "gitlab:*", Params: json.RawMessage(`{"mode":"deny","min_words":120,"max_denials":1}`)},
		{ID: uuid.New(), Pattern: "gitlab:comment*", Params: json.RawMessage(`{"mode":"approval","min_words":40}`)},
	}
	rule, p := strictestStyleGate(gates)
	if rule.Pattern != "gitlab:*" || p.Mode != "deny" || p.MinWords != 40 || p.MaxDenials != 1 {
		t.Fatalf("got %s %+v", rule.Pattern, p)
	}
}

func TestDenialReasonNamesTheFindings(t *testing.T) {
	text := "Die Digitalisierung stellt für mittelständische Unternehmen eine zentrale Herausforderung dar, deren Bewältigung eine ganzheitliche Betrachtung der bestehenden Prozesse sowie eine nachhaltige Strategieentwicklung erfordert. Grundsätzlich ist die Umsetzung digitaler Transformationsprozesse ein vielschichtiges Unterfangen.\n\nIn diesem Zusammenhang ist es entscheidend, dass die Implementierung entsprechender Maßnahmen unter Berücksichtigung der individuellen Rahmenbedingungen erfolgt."
	profile := style.Profile{Language: "de", Bands: map[string][2]float64{"nominalisation_rate": {0, 4}, "para_without_anchor_share": {0, 25}}}
	r := style.Check(text, &profile)
	if r.Score == 0 {
		t.Fatal("the generic text passed")
	}
	reason := denialReason("gitlab:comment*", r)
	for _, want := range []string{"style gate (gitlab:comment*)", "nominalisations per 100 words", "Paragraphs to revise", "Die Digitalisierung stellt"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason lacks %q:\n%s", want, reason)
		}
	}
	out := withStyleFindings(json.RawMessage(`{"body":"x"}`), r)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil || m["body"] != "x" || m["style_findings"] == nil {
		t.Fatalf("withStyleFindings: %s %v", out, err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
