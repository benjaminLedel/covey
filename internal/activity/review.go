package activity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"covey/internal/llm"
)

// ErrEmptyReview: the model answered with nothing.
var ErrEmptyReview = errors.New("the model returned no review")

// ErrNothingToReview: the day has no sessions.
var ErrNothingToReview = errors.New("no activity recorded for that day")

// reviewSystem is the whole instruction. What carries it: the review is the
// person's own record, so it states what the sessions show and nothing else —
// a reconstruction that invents a meeting is worse than a gap.
const reviewSystem = `You write a person's daily review from their own activity log: sessions of work their computer recorded — app, window title, web address, the field they worked in and an excerpt of its content, each with a start and an end.

Write in the language given. Output Markdown:

1. One or two sentences: what the day was mostly about.
2. "## " and a heading for the timeline, then the day as a short list of activities in order, one line each: the time span, then what was done — "09:10–09:40 Answered Ada's mail about the offer". Merge sessions that belong to one activity (switching between a mail and the document it is about is one activity). Leave out fleeting sessions that show nothing.
3. If the log shows things begun but not finished — a draft left open, a question not answered — "## " and a heading for open items, as a checklist ("- [ ] …"). Otherwise leave this section out.

Use only what the log shows. Do not guess intentions, rate the day, or give advice. Quote no more of an excerpt than a few words where it names the subject; never passwords, codes or anything that looks like a secret.`

// Review writes the daily review of sessions — one tool-less control-plane
// turn. lang is the language to write in ("de", "en", …).
func Review(ctx context.Context, p llm.Provider, day time.Time, sessions []Session, lang string, loc *time.Location) (string, error) {
	if len(sessions) == 0 {
		return "", ErrNothingToReview
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Language: %s\nDay: %s\n\nSessions:\n", lang, day.In(loc).Format("2006-01-02 (Monday)"))
	for _, s := range sessions {
		fmt.Fprintf(&b, "- %s–%s %s", s.StartedAt.In(loc).Format("15:04"), s.EndedAt.In(loc).Format("15:04"), s.App)
		if s.Window != "" {
			fmt.Fprintf(&b, " · window %q", s.Window)
		}
		if s.URL != "" {
			fmt.Fprintf(&b, " · %s", s.URL)
		}
		if s.Field != "" {
			fmt.Fprintf(&b, " · field %q", s.Field)
		}
		if ex := strings.TrimSpace(s.Excerpt); ex != "" {
			if r := []rune(ex); len(r) > 300 {
				ex = string(r[len(r)-300:])
			}
			fmt.Fprintf(&b, " · content %q", ex)
		}
		b.WriteString("\n")
	}
	out, err := p.Complete(ctx, llm.Request{
		Tier:      llm.TierBest,
		Effort:    "low",
		MaxTokens: 3000,
		System:    reviewSystem,
		Messages:  []llm.Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", ErrEmptyReview
	}
	return out, nil
}
