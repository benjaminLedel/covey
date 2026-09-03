package style

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReviseIteratesUntilCleanAndKeepsBest(t *testing.T) {
	fx := loadFixtures(t)
	generic, concrete := fx.Texts["generic_de"].Text, fx.Texts["concrete_de"].Text
	step1 := strings.Replace(generic,
		"Die Digitalisierung stellt für mittelständische Unternehmen eine zentrale Herausforderung dar, deren Bewältigung eine ganzheitliche Betrachtung der bestehenden Prozesse sowie eine nachhaltige Strategieentwicklung erfordert. Grundsätzlich ist die Umsetzung digitaler Transformationsprozesse ein vielschichtiges Unterfangen, das nicht nur technologische, sondern auch organisatorische und kulturelle Dimensionen umfasst.",
		"Thomas Kern führt eine Schreinerei mit 14 Mitarbeitern. Bis 2023 stand jeder Auftrag auf einem Zettel. Zwei von zehn Zetteln gingen verloren.", 1)
	if step1 == generic {
		t.Fatal("fixture text changed; the replacement did not apply")
	}

	outputs := []string{step1, concrete}
	var systems, users []string
	call := func(_ context.Context, system, user string) (string, error) {
		systems = append(systems, system)
		users = append(users, user)
		out := outputs[0]
		outputs = outputs[1:]
		return out, nil
	}
	// The material whitelists the facts the fake reviser brings in.
	res, err := Revise(context.Background(), ReviseInput{Text: generic, Material: concrete, Prose: "## Voice\n\n1. Konkret."}, call)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != "no findings left" || len(res.Iterations) != 2 || res.Best.Score != 0 {
		t.Fatalf("stop %q, iterations %+v, best %d", res.StopReason, res.Iterations, res.Best.Score)
	}
	if strings.TrimSpace(res.Text) != strings.TrimSpace(concrete) {
		t.Fatal("the best text is the clean one")
	}
	if res.Iterations[0].ScoreAfter >= res.Iterations[0].ScoreBefore {
		t.Fatalf("the first iteration has to improve: %+v", res.Iterations[0])
	}
	if !strings.Contains(systems[0], "You are an editor") || !strings.Contains(systems[0], "Konkret") {
		t.Fatal("the system prompt carries the rules and the voice")
	}
	if !strings.Contains(users[0], "<findings>") || !strings.Contains(users[0], "<material>") {
		t.Fatal("the user message carries findings and material")
	}
}

func TestReviseStopsWhenNothingImprovesAndOnInventedFacts(t *testing.T) {
	fx := loadFixtures(t)
	generic := fx.Texts["generic_de"].Text
	// A reviser that returns the same text: one iteration, no improvement.
	same := func(_ context.Context, _, _ string) (string, error) { return generic, nil }
	res, err := Revise(context.Background(), ReviseInput{Text: generic, MaxIter: 5}, same)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Iterations) != 1 || !strings.Contains(res.StopReason, "did not improve") || res.Text != generic {
		t.Fatalf("stop %q, iterations %d", res.StopReason, len(res.Iterations))
	}
	// A reviser that invents a number without material: the claim counts against it.
	invent := func(_ context.Context, _, _ string) (string, error) {
		return strings.Replace(generic, "Die Digitalisierung stellt", "Die Digitalisierung kostete 2023 rund 4.500 Euro und stellt", 1), nil
	}
	res, err = Revise(context.Background(), ReviseInput{Text: generic}, invent)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Iterations) != 1 || res.Iterations[0].Note != "no improvement" || res.Best.Claims != nil && len(res.Best.Claims.Added) > 0 {
		// The best version stays the original (its report has no claim diff).
		t.Fatalf("invented facts must not count as progress: %+v best=%+v", res.Iterations, res.Best.Claims)
	}
	// A failing model surfaces as an error, with the best so far intact.
	fail := func(_ context.Context, _, _ string) (string, error) { return "", errors.New("boom") }
	res, err = Revise(context.Background(), ReviseInput{Text: generic}, fail)
	if err == nil || !strings.Contains(err.Error(), "boom") || res.Text != generic {
		t.Fatalf("err %v, text kept %v", err, res.Text == generic)
	}
	// Nothing to do: no call.
	concrete := fx.Texts["concrete_de"].Text
	called := false
	res, err = Revise(context.Background(), ReviseInput{Text: concrete}, func(context.Context, string, string) (string, error) { called = true; return "", nil })
	if err != nil || called || res.StopReason != "nothing to do" {
		t.Fatalf("clean text: err %v called %v stop %q", err, called, res.StopReason)
	}
	if Unfence("```markdown\nhello\n```") != "hello\n" || Unfence("plain") != "plain\n" {
		t.Fatal("Unfence")
	}
	if p := DefaultProfile("en"); p.Language != "en" || p.Bands["commas_per_sentence"][1] != 1.3 {
		t.Fatalf("DefaultProfile en: %+v", p.Bands["commas_per_sentence"])
	}
}
