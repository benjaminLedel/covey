package dictation

import (
	"context"
	"strings"
	"testing"

	"covey/internal/llm"
)

type fakeModel struct {
	got    llm.Request
	answer string
}

func (f *fakeModel) Name() string { return "fake" }

func (f *fakeModel) Complete(_ context.Context, req llm.Request) (string, error) {
	f.got = req
	return f.answer, nil
}

func TestCleanSendsTheDictationAndTheTargetApp(t *testing.T) {
	m := &fakeModel{answer: "  Wir treffen uns am Mittwoch.\n"}
	out, err := Clean(context.Background(), m, "äh wir treffen uns am Dienstag nein Mittwoch", "Mail")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Wir treffen uns am Mittwoch." {
		t.Fatalf("the answer is kept, trimmed: %q", out)
	}
	msg := m.got.Messages[0].Content
	if !strings.Contains(msg, "Dienstag nein Mittwoch") || !strings.HasPrefix(msg, "Target application: Mail") {
		t.Fatalf("dictation and app must reach the model: %q", msg)
	}
	if m.got.Effort != "" || !m.got.NoThinking {
		t.Fatal("the fast model takes no effort parameter, and thinking only costs time here")
	}
	if m.got.Tier != llm.TierFast {
		t.Fatal("cleanup is a fast turn: the person waits with the cursor blinking")
	}
	if !strings.Contains(m.got.System, "self-corrections") || !strings.Contains(m.got.System, "add anything that was not said") {
		t.Fatal("the instruction must apply corrections and forbid invention")
	}
}

func TestCleanWithoutAnAppSaysNone(t *testing.T) {
	m := &fakeModel{answer: "x"}
	if _, err := Clean(context.Background(), m, "hallo", " "); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.got.Messages[0].Content, "Target application") {
		t.Fatal("no app, no line about one")
	}
}

func TestCleanRefusesAnEmptyAnswer(t *testing.T) {
	if _, err := Clean(context.Background(), &fakeModel{answer: "\n"}, "x", ""); err != ErrEmpty {
		t.Fatalf("an empty answer is not a text: %v", err)
	}
}
