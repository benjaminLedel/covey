package notes

import (
	"context"
	"errors"
	"strings"

	"covey/internal/llm"
)

// ErrEmptySummary: the model answered with nothing worth storing.
var ErrEmptySummary = errors.New("the model returned no summary")

// summarizeSystem is the whole instruction. Two things carry it: the answer
// is in the language of the transcript, and nothing appears in it that the
// transcript does not say — a summary that invents an action item is worse
// than none, because the person trusts it instead of rereading.
const summarizeSystem = `You summarise a transcript that a person recorded for themselves — usually a meeting, sometimes a spoken note.

Write in the language of the transcript. Output Markdown with exactly two sections, with headings in that language:

1. A summary: three to six sentences on what was discussed and decided.
2. Action items: a checklist ("- [ ] …"), one line each, with the person responsible when the transcript names one. If there are none, say so in one line.

Use only what the transcript says. Do not add advice, context or items that were not said. Speech recognition makes mistakes; where a word is clearly misheard, write what was meant, and where it is unclear, leave it out rather than guess.`

// summarizeMaxTokens: a summary and a checklist, not an essay.
const summarizeMaxTokens = 2000

// Summarize turns a transcript into a summary and a list of action items —
// one tool-less turn in the control plane (internal/llm), no sandbox.
func Summarize(ctx context.Context, p llm.Provider, transcript string) (string, error) {
	out, err := p.Complete(ctx, llm.Request{
		Tier:      llm.TierBest,
		Effort:    "low",
		MaxTokens: summarizeMaxTokens,
		System:    summarizeSystem,
		Messages:  []llm.Message{{Role: "user", Content: "Transcript:\n\n" + transcript}},
	})
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", ErrEmptySummary
	}
	return out, nil
}
