package activity

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestReviewListsTheSessionsInTheDaysTime(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	start := time.Date(2026, 9, 26, 7, 10, 0, 0, time.UTC) // 09:10 in Berlin
	m := &fakeModel{answer: "\nEin Tag mit Mails.\n"}
	long := strings.Repeat("x", 400) + "ENDE"
	out, err := Review(context.Background(), m, start, []Session{
		{StartedAt: start, EndedAt: start.Add(30 * time.Minute), App: "Mail", Window: "Re: Angebot", Field: "text area", Excerpt: long},
		{StartedAt: start.Add(40 * time.Minute), EndedAt: start.Add(50 * time.Minute), App: "Safari", URL: "https://example.org/offer"},
	}, "de", berlin)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Ein Tag mit Mails." {
		t.Fatalf("the answer is kept, trimmed: %q", out)
	}
	msg := m.got.Messages[0].Content
	for _, want := range []string{"Language: de", "2026-09-26", "09:10–09:40 Mail", `window "Re: Angebot"`, "10:00", "https://example.org/offer", "ENDE"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("%q missing from %q", want, msg)
		}
	}
	if strings.Contains(msg, strings.Repeat("x", 301)) {
		t.Fatal("an excerpt goes in as its last 300 characters, not whole")
	}
	if !strings.Contains(m.got.System, "Use only what the log shows") {
		t.Fatal("the instruction must forbid invention")
	}
	if !strings.Contains(m.got.System, "what an agent could take over") {
		t.Fatal("the review names work an agent could take over (#370)")
	}
}

func TestReviewOfAnEmptyDay(t *testing.T) {
	if _, err := Review(context.Background(), &fakeModel{answer: "x"}, time.Now(), nil, "de", time.UTC); err != ErrNothingToReview {
		t.Fatalf("an empty day is not reviewed: %v", err)
	}
}

func TestValidSessions(t *testing.T) {
	now := time.Now()
	ok := Session{StartedAt: now, EndedAt: now.Add(time.Minute), App: "Mail"}
	if !valid(ok) {
		t.Fatal("a plain session is valid")
	}
	for name, s := range map[string]Session{
		"no start":        {EndedAt: now},
		"ends before":     {StartedAt: now, EndedAt: now.Add(-time.Second)},
		"a day and more":  {StartedAt: now, EndedAt: now.Add(25 * time.Hour)},
		"a novel excerpt": {StartedAt: now, EndedAt: now, Excerpt: strings.Repeat("x", 1001)},
	} {
		if valid(s) {
			t.Fatalf("%s: accepted", name)
		}
	}
}
