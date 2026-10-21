package orchestrator

import "testing"

// TestReplyWithQuestion is #650: a parked conversation task posts its reply,
// and the question once — appended only where the reply does not ask it.
func TestReplyWithQuestion(t *testing.T) {
	cases := []struct{ reply, question, want string }{
		{"", "Welche?", "Welche?"},
		{"Zwei Vorschläge: 1. A, 2. B.\n\nWelche der beiden soll ich entwerfen?", "Welche der beiden soll ich entwerfen?",
			"Zwei Vorschläge: 1. A, 2. B.\n\nWelche der beiden soll ich entwerfen?"},
		{"Zwei Vorschläge: 1. A, 2. B.\nwelche der beiden   soll ich entwerfen", "Welche der beiden soll ich entwerfen?",
			"Zwei Vorschläge: 1. A, 2. B.\nwelche der beiden   soll ich entwerfen"},
		{"Die Datei ist nicht angekommen.", "Kannst du sie noch einmal anhängen?",
			"Die Datei ist nicht angekommen.\n\nKannst du sie noch einmal anhängen?"},
		{"Erledigt.", "", "Erledigt."},
	}
	for _, c := range cases {
		if got := replyWithQuestion(c.reply, c.question); got != c.want {
			t.Errorf("replyWithQuestion(%q, %q) = %q, want %q", c.reply, c.question, got, c.want)
		}
	}
}
