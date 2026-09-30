package normalise

import "testing"

// TestLanguage: the detector of #511 is sure or says nothing. The first four
// lines are from a German call whose spoken forms came out in English.
func TestLanguage(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"Hi! Bin gerade bei den Reviews – schau mir die MRs an und warte auf die Pipelines.", "de"},
		{"Hi! I'm looking at the merge requests right now and waiting for the pipelines to finish.", "en"},
		{"Nein, Holländisch kann ich nicht. Warum?", "de"},
		{"No, I don't speak Dutch. Why do you ask?", "en"},
		{"Now you're suddenly speaking English.", "en"},
		{"Jetzt sprichst du plötzlich Englisch.", "de"},
		{"Je suis en train de regarder les tickets, c'est presque fini.", "fr"},
		{"Estoy mirando los tickets ahora, pero no hay nada nuevo.", "es"},
		{"Ik ben nog bezig met de tickets, maar het is bijna klaar.", "nl"},
		{"今日はチケットを見ています。", "ja"},
		{"我正在看这些工单。", "zh"},
		// Too short, or nothing to tell by.
		{"Hi!", ""},
		{"Okay.", ""},
		{"👍", ""},
		{"DLES-273, MR !475", ""},
		{"", ""},
	} {
		if got := Language(c.text); got != c.want {
			t.Errorf("Language(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestSameLanguage(t *testing.T) {
	de := "Hi! Bin gerade bei den Reviews – schau mir die MRs an und warte auf die Pipelines."
	if SameLanguage(de, "Hi! I'm looking at the merge requests right now and waiting for the pipelines to finish.") {
		t.Error("German and English read as the same language")
	}
	if !SameLanguage(de, "Ich schau mir gerade die Merge Requests an und warte auf die Pipelines.") {
		t.Error("two German texts read as different languages")
	}
	if !SameLanguage(de, "Okay.") || !SameLanguage("👍", "Alles klar, mach ich.") {
		t.Error("a text that cannot be told must not count as another language")
	}
}
