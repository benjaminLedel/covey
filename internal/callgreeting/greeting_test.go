package callgreeting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"covey/internal/llm"
)

type fakeModel struct {
	got    llm.Request
	answer string
	err    error
}

func (f *fakeModel) Name() string { return "fake" }

func (f *fakeModel) Complete(_ context.Context, req llm.Request) (string, error) {
	f.got = req
	return f.answer, f.err
}

func berlin(t *testing.T) *time.Location {
	t.Helper()
	return time.FixedZone("CEST", 2*60*60)
}

func TestThePromptCarriesTheContext(t *testing.T) {
	loc := berlin(t)
	now := time.Date(2026, 9, 28, 9, 12, 0, 0, loc) // a Monday
	c := Context{
		AgentName: "Paula", Department: "Content", FirstName: "Ada", FullName: "Ada Berger",
		Language: "de", Local: now, Address: "du", Tone: "casual",
		InProgress: []string{"Übersicht Kundenumfrage", "Blogartikel Herbst", "Newsletter Oktober", "a fourth one"},
		DoneToday:  "Pressemitteilung verschickt",
		Recent: []Line{
			{Author: "Ada", Text: "Kannst du die Umfrage auswerten? https://example.org/x", At: now.Add(-40 * time.Minute)},
			{Agent: true, Author: "Paula", Text: "Mach ich.", At: time.Date(2026, 9, 27, 15, 2, 0, 0, time.UTC)},
		},
	}
	sys, user := Prompt(c)
	for _, want := range []string{
		"You are: Paula (department: Content)", "The caller: Ada", "The caller's full name: Ada Berger",
		`Address: informal (German "du")`, "Tone: casual", "Language: de",
		"The caller's local time: Monday, 09:12 (morning)",
		"- Übersicht Kundenumfrage", "- Newsletter Oktober", "You finished today: Pressemitteilung verschickt",
		"- today 08:32 (morning), Ada: Kannst du die Umfrage auswerten?", "- yesterday 17:02, you: Mach ich.",
	} {
		if !strings.Contains(user, want) {
			t.Fatalf("%q missing from the prompt:\n%s", want, user)
		}
	}
	if strings.Contains(user, "a fourth one") || strings.Contains(user, "example.org") {
		t.Fatalf("at most three tasks, and no links:\n%s", user)
	}
	for _, want := range []string{"180 characters", "open question", "no ticket numbers, ids", "Guten Morgen, Ada!", "Morning, Ada"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("%q missing from the instruction", want)
		}
	}
}

func TestThePromptOfAFirstCallSaysSo(t *testing.T) {
	_, user := Prompt(Context{AgentName: "Paula", Local: time.Date(2026, 9, 26, 20, 0, 0, 0, time.UTC), Weekday: "Samstag"})
	if !strings.Contains(user, "no earlier messages") || !strings.Contains(user, "Samstag, 20:00 (evening)") {
		t.Fatalf("a first call, with the app's weekday:\n%s", user)
	}
	if strings.Contains(user, "Address") || strings.Contains(user, "working on") {
		t.Fatalf("what is not known is left out:\n%s", user)
	}
}

func TestWriteIsAFastTurnAndNormalisesTheAnswer(t *testing.T) {
	m := &fakeModel{answer: "  „Guten Morgen, Ada! Wir hatten um 10:30 über die Umfrage gesprochen, weiter damit?“ 😊\n"}
	out, err := Write(context.Background(), m, Context{AgentName: "Paula", Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if m.got.Tier != llm.TierFast || !m.got.NoThinking {
		t.Fatal("the greeting is a fast turn without thinking: the line is ringing")
	}
	if strings.Contains(out, "10:30") || strings.Contains(out, "😊") || strings.Contains(out, "„") || !strings.HasSuffix(out, "?") {
		t.Fatalf("guarded and normalised for speech: %q", out)
	}
	if !strings.HasPrefix(out, "Guten Morgen, Ada!") {
		t.Fatalf("the greeting itself stays: %q", out)
	}
}

func TestWritePassesTheModelsError(t *testing.T) {
	if _, err := Write(context.Background(), &fakeModel{err: context.DeadlineExceeded}, Context{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the model's error: %v", err)
	}
}

func TestGuard(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Hallo Ada! Was kann ich für dich tun?", "Hallo Ada! Was kann ich für dich tun?"},
		{"Greeting: Hi Ada, what's up?", "Hi Ada, what's up?"},
		{"Hi Ada! What can I do for you? I'm here all day.", "Hi Ada! What can I do for you?"},
		{"**Hi** Ada 👋, what can I do for you?", "Hi Ada, what can I do for you?"},
		{"こんにちは、アダさん！何か手伝えることある？", "こんにちは、アダさん！何か手伝えることある？"},
	} {
		got, err := Guard(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("Guard(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{
		"",
		"Hallo Ada, schön dich zu hören.",
		"Hallo Ada. Ich arbeite an der Umfrage. Und am Newsletter. Was gibt's?",
		"Hi Ada, the ticket DLES-273 is done. Anything else?",
		"Hi Ada, see https://example.org. Anything else?",
		"Hi Ada, task 3f2a9c1e-77aa-4b1c-9d2e-0a1b2c3d4e5f is done. Anything else?",
		"Hi Ada, merge request !475 is waiting. Anything else?",
		"Hi Ada! " + strings.Repeat("I have been working on a great many things today, ", 4) + "what can I do for you?",
	} {
		if got, err := Guard(in); !errors.Is(err, ErrRejected) {
			t.Fatalf("Guard(%q) = %q, want it refused", in, got)
		}
	}
}

func TestFirstName(t *testing.T) {
	for in, want := range map[string]string{"Ada Berger": "Ada", " ada ": "ada", "ada@example.org": "ada", "": ""} {
		if got := FirstName(in); got != want {
			t.Fatalf("FirstName(%q) = %q, want %q", in, got, want)
		}
	}
}
