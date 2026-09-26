package activity

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCondenseGroupsByDayAppAndPlace(t *testing.T) {
	at := func(d, h, m int) time.Time { return time.Date(2026, 9, d, h, m, 0, 0, time.UTC) }
	out := Condense([]Session{
		{StartedAt: at(24, 7, 0), EndedAt: at(24, 7, 20), App: "Chrome", URL: "https://support.example.org/tickets/1", Window: "Ticket 1"},
		{StartedAt: at(24, 7, 20), EndedAt: at(24, 7, 30), App: "Chrome", URL: "https://support.example.org/tickets/2", Window: "Ticket 2", Excerpt: "Vielen Dank für Ihre Anfrage"},
		{StartedAt: at(24, 8, 0), EndedAt: at(24, 8, 0).Add(20 * time.Second), App: "Finder"},
		{StartedAt: at(25, 7, 5), EndedAt: at(25, 7, 35), App: "Chrome", URL: "https://support.example.org/tickets/3"},
	}, time.UTC)
	if !strings.Contains(out, "2026-09-24 Thu\n- from 07:00, 30 min, Chrome · support.example.org") {
		t.Fatalf("one line per day, app and host, with the minutes summed: %s", out)
	}
	if !strings.Contains(out, `content "Vielen Dank für Ihre Anfrage"`) || !strings.Contains(out, "2026-09-25 Fri") {
		t.Fatalf("excerpts and every day: %s", out)
	}
	if strings.Contains(out, "Finder") {
		t.Fatalf("under a minute is noise: %s", out)
	}
}

func TestSuggestReadsJSONWithOrWithoutAFence(t *testing.T) {
	m := &fakeModel{answer: "```json\n[{\"title\":\"Tickets sortieren\",\"brief\":\"Ein Support-Agent …\",\"minutes_per_week\":150,\"systems\":[\"Zendesk\"]},{\"title\":\"ohne Ausschreibung\"}]\n```"}
	got, err := Suggest(context.Background(), m, []Session{{StartedAt: time.Now(), EndedAt: time.Now().Add(time.Hour), App: "Chrome"}}, "de", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Tickets sortieren" || got[0].MinutesPerWeek != 150 {
		t.Fatalf("a suggestion without a posting is dropped: %+v", got)
	}
	if !strings.Contains(m.got.System, "Better no suggestion than a weak one") || !strings.HasPrefix(m.got.Messages[0].Content, "Language: de") {
		t.Fatal("the instruction and the language")
	}
	if _, err := parseSuggestions("I could not find anything."); err == nil {
		t.Fatal("prose is not an answer")
	}
	if s, err := parseSuggestions("[]"); err != nil || len(s) != 0 {
		t.Fatalf("an empty list is an answer: %v %v", s, err)
	}
}
