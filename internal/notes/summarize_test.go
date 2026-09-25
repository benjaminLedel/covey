package notes

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

func TestSummarizeSendsTheTranscriptAndKeepsTheAnswer(t *testing.T) {
	m := &fakeModel{answer: "\n## Zusammenfassung\nBudget steht.\n\n## Aufgaben\n- [ ] Ada schickt das Angebot\n"}
	out, err := Summarize(context.Background(), m, "Ada: Ich schicke das Angebot bis Freitag.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "## Zusammenfassung") || strings.HasSuffix(out, "\n") {
		t.Fatalf("the answer is kept, trimmed: %q", out)
	}
	if !strings.Contains(m.got.Messages[0].Content, "Ich schicke das Angebot") {
		t.Fatal("the transcript must reach the model")
	}
	if !strings.Contains(m.got.System, "language of the transcript") || !strings.Contains(m.got.System, "Use only what the transcript says") {
		t.Fatal("the instruction must pin the language and forbid invention")
	}
}

func TestSummarizeRefusesAnEmptyAnswer(t *testing.T) {
	if _, err := Summarize(context.Background(), &fakeModel{answer: "  \n"}, "x"); err != ErrEmptySummary {
		t.Fatalf("an empty answer is not a summary: %v", err)
	}
}
