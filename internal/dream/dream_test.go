package dream

import (
	"strings"
	"testing"

	"covey/internal/memory"
)

func TestClampStoryLeavesShortStoryAlone(t *testing.T) {
	story := "Es träumte von zwei Gestalten, die eine wurden."
	if got := clampStory(story); got != story {
		t.Fatalf("short story was changed: %q", got)
	}
}

func TestClampStoryCutsAtTheLastSentenceEnd(t *testing.T) {
	// A first sentence well past the 200-rune floor, then a second one that
	// runs over the cap: the cut has to land on the first sentence's full stop.
	first := strings.Repeat("a", 400) + ". "
	story := first + strings.Repeat("b", storyMaxChars)
	got := clampStory(story)
	if !strings.HasSuffix(got, ".") {
		t.Fatalf("cut did not land on a sentence end: %q", got[len(got)-40:])
	}
	if len([]rune(got)) != len([]rune(strings.TrimSpace(first))) {
		t.Fatalf("cut at the wrong place: %d runes, expected %d", len([]rune(got)), len([]rune(strings.TrimSpace(first))))
	}
}

func TestClampStoryWithoutSentenceEndAppendsEllipsis(t *testing.T) {
	story := strings.Repeat("x", storyMaxChars+100)
	got := clampStory(story)
	if !strings.HasSuffix(got, " …") {
		t.Fatalf("no ellipsis appended: %q", got[len(got)-10:])
	}
	if len([]rune(got)) != storyMaxChars+2 {
		t.Fatalf("length %d, expected %d", len([]rune(got)), storyMaxChars+2)
	}
}

func TestClampStoryIgnoresAnEarlySentenceEnd(t *testing.T) {
	// The only full stop sits below the 200-rune floor — cutting there would
	// throw the story away, so the ellipsis is the better answer.
	story := "Kurz. " + strings.Repeat("y", storyMaxChars+100)
	got := clampStory(story)
	if !strings.HasSuffix(got, " …") {
		t.Fatalf("early full stop was used as the cut: %q", got)
	}
}

func TestRetitlePromptCarriesSlugTitleAndShortenedBody(t *testing.T) {
	pages := []memory.Entry{
		{Slug: "kunde-meier", Title: "Störung bei Meier am 30.07.", Content: "Zeile eins\n\nZeile   zwei"},
		{Slug: "lang", Title: "Lang", Content: strings.Repeat("z", bodyChars+50)},
	}
	got := retitlePrompt(pages)
	for _, want := range []string{"--- slug: kunde-meier", "title: Störung bei Meier am 30.07.", "content: Zeile eins Zeile zwei"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, strings.Repeat("z", bodyChars)+" …") {
		t.Fatal("long body was not shortened to bodyChars")
	}
	if strings.Contains(got, strings.Repeat("z", bodyChars+1)) {
		t.Fatal("long body reached the prompt in full")
	}
}

func TestParseRetitleAcceptsAFencedAnswerWithProse(t *testing.T) {
	known := map[string]memory.Entry{"kunde-meier": {Slug: "kunde-meier", Title: "Störung bei Meier am 30.07."}}
	raw := "Hier das Ergebnis:\n```json\n{\"proposals\":[{\"slug\":\"kunde-meier\",\"title\":\"Kunde Meier\",\"reason\":\" nennt die Sache \"}]}\n```\n"
	got := parseRetitle(raw, known)
	if len(got) != 1 {
		t.Fatalf("got %d proposals, expected 1: %#v", len(got), got)
	}
	if got[0].Title != "Kunde Meier" || got[0].Reason != "nennt die Sache" {
		t.Fatalf("proposal not trimmed as expected: %#v", got[0])
	}
}

func TestParseRetitleDiscardsWhatDoesNotFixTheFinding(t *testing.T) {
	known := map[string]memory.Entry{
		"a": {Slug: "a", Title: " Alt "},
		"b": {Slug: "b", Title: "Auch alt"},
		"c": {Slug: "c", Title: "Drittens"},
	}
	raw := `{"proposals":[
	  {"slug":"unbekannt","title":"Erfunden"},
	  {"slug":"a","title":"Alt"},
	  {"slug":"b","title":"  "},
	  {"slug":"c","title":"Störung am 30.07.2026 behoben"}
	]}`
	if got := parseRetitle(raw, known); len(got) != 0 {
		t.Fatalf("expected no proposal to survive, got %#v", got)
	}
}

func TestParseRetitleKeepsOnlyTheFirstProposalPerSlug(t *testing.T) {
	known := map[string]memory.Entry{"a": {Slug: "a", Title: "Alt"}}
	raw := `{"proposals":[{"slug":"a","title":"Neu"},{"slug":"a","title":"Noch neuer"}]}`
	got := parseRetitle(raw, known)
	if len(got) != 1 || got[0].Title != "Neu" {
		t.Fatalf("duplicate slug not discarded: %#v", got)
	}
}

func TestParseRetitleOnUnreadableAnswer(t *testing.T) {
	if got := parseRetitle("das Modell hat geplaudert", nil); got != nil {
		t.Fatalf("expected nil for unreadable JSON, got %#v", got)
	}
}

func TestParseClock(t *testing.T) {
	hh, mm, err := parseClock(" 03:15 ")
	if err != nil || hh != 3 || mm != 15 {
		t.Fatalf("parseClock = %d:%d, %v", hh, mm, err)
	}
	if _, _, err := parseClock("25:00"); err == nil {
		t.Fatal("expected an error for 25:00")
	}
}

func TestHeadTruncatesRuneSafely(t *testing.T) {
	if got := head("  ätherisch  ", 3); got != "äth…" {
		t.Fatalf("head = %q", got)
	}
	if got := head(" kurz ", 10); got != "kurz" {
		t.Fatalf("head = %q", got)
	}
}
