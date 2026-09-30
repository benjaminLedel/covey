package dictation

import (
	"context"
	"strings"
	"testing"
)

// TestCleanTurnShortIsRaw: a turn under MinTurnWords is not cleaned — the
// model is not asked. The observed case (#511): "Hallo" came back as "Ja.".
func TestCleanTurnShortIsRaw(t *testing.T) {
	m := &fakeModel{answer: "Ja."}
	out, kept, err := CleanTurn(context.Background(), m, "Hallo", Context{})
	if err != nil || out != "Hallo" || kept != KeptShort {
		t.Fatalf("%q %q %v", out, kept, err)
	}
	if m.got.System != "" {
		t.Fatal("a short turn reached the model")
	}
	if TurnEdit("Hallo", "Ja.") <= MaxTurnEdit {
		t.Fatal(`"Hallo" → "Ja." passes the edit bound`)
	}
}

// TestCleanTurnCorrects: a correction of punctuation, capitals and a
// misheard name passes; the instruction keeps every word and the language.
func TestCleanTurnCorrects(t *testing.T) {
	m := &fakeModel{answer: "Hat Gertrud den Merge Request schon angeschaut?"}
	out, kept, err := CleanTurn(context.Background(), m, "hat gertrut den merge request schon angeschaut", Context{Field: "Names in this conversation: Gertrud"})
	if err != nil || kept != "" || out != "Hat Gertrud den Merge Request schon angeschaut?" {
		t.Fatalf("%q %q %v", out, kept, err)
	}
	for _, want := range []string{"Keep every word", "Never translate", "spelling, punctuation"} {
		if !strings.Contains(m.got.System, want) {
			t.Fatalf("%q missing from the instruction:\n%s", want, m.got.System)
		}
	}
}

// TestCleanTurnTranslatedIsRaw: a clean-up that translated the turn is
// not sent (#511): the person's German turns reached the conversation in
// English.
func TestCleanTurnTranslatedIsRaw(t *testing.T) {
	raw := "jetzt sprichst du plötzlich englisch mit mir"
	m := &fakeModel{answer: "Now you're suddenly speaking English with me."}
	out, kept, err := CleanTurn(context.Background(), m, raw, Context{})
	if err != nil || out != raw || kept != KeptLanguage {
		t.Fatalf("%q %q %v", out, kept, err)
	}
}

// TestCleanTurnRewrittenIsRaw: a clean-up that changed the words, not
// their spelling, is not sent.
func TestCleanTurnRewrittenIsRaw(t *testing.T) {
	raw := "kannst du mal nach den pipelines schauen"
	m := &fakeModel{answer: "Ja, die Pipelines laufen alle."}
	out, kept, err := CleanTurn(context.Background(), m, raw, Context{})
	if err != nil || out != raw || kept != KeptEdited {
		t.Fatalf("%q %q %v", out, kept, err)
	}
}

func TestTurnEdit(t *testing.T) {
	for _, c := range []struct {
		raw, cleaned string
		max          float64
		over         bool
	}{
		{"hat gertrut den merge request schon angeschaut", "Hat Gertrud den Merge Request schon angeschaut?", 0.1, false},
		{"äh kannst du mal schauen", "Kannst du mal schauen?", 0.2, false},
		{"kannst du mal nach den pipelines schauen", "Ja, die Pipelines laufen alle.", 1, true},
		{"Hallo", "Ja.", 1, true},
	} {
		got := TurnEdit(c.raw, c.cleaned)
		if got > c.max || (got > MaxTurnEdit) != c.over {
			t.Errorf("TurnEdit(%q, %q) = %.2f", c.raw, c.cleaned, got)
		}
	}
}

// TestCleanTranslatedIsRaw: a dictation the model translated is returned
// as recognised (#511), and the instruction says to keep the language.
func TestCleanTranslatedIsRaw(t *testing.T) {
	raw := "wir treffen uns am mittwoch um zehn und besprechen das angebot"
	m := &fakeModel{answer: "We are meeting on Wednesday at ten to discuss the offer."}
	out, err := Clean(context.Background(), m, raw, "Mail", Context{})
	if err != nil || out != raw {
		t.Fatalf("%q %v", out, err)
	}
	if !strings.Contains(m.got.System, "Never translate") {
		t.Fatal("the instruction must forbid translating")
	}
}
