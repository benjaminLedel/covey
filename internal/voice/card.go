package voice

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"covey/internal/llm"
	"covey/internal/style"
)

// The card is the one artefact of a voice that a model writes, and the one a
// person has to release before it acts.
//
// Why a model at all: bands and exemplars are evidence, and neither says what
// the author DOES. A description in words is what a model can follow while
// writing — "opens on the observation, not on the topic" steers a draft where
// "sentence length 14 to 24" only grades it afterwards.
//
// Why a person releases it: a description of somebody's hand, written by a
// model, is a claim about a person. It goes into the prompt of every run of
// every agent carrying the voice, and a wrong sentence in it is repeated for as
// long as nobody reads it. So a build proposes and a person accepts — and a
// released card is not overwritten by the next build (the corpus may grow; the
// description somebody signed stays).
//
// Every claim has to name a passage. That is what keeps the card from becoming
// the horoscope a model writes about any corpus: a sentence that cannot quote
// what it is about is left out rather than softened.

// cardWords is the room the card gets. Long enough to describe a hand, short
// enough to sit in every prompt: it is paid per run, like the exemplars.
const cardWords = 400

// CardPrompt is what the model is asked. Exported so the same text can be read
// in a test and in the interface — whoever releases a card should be able to
// see what was asked for it.
func CardPrompt(b Built, name string) string {
	var sb strings.Builder
	lang := b.Profile.Language
	fmt.Fprintf(&sb, "Describe the hand of the author whose texts are measured below. "+
		"Write the description IN THE LANGUAGE OF THE CORPUS (%s), 250 to %d words, as continuous prose "+
		"under these five headings: opening, argument, facts, sentences and paragraphs, and never.\n\n", lang, cardWords)
	sb.WriteString("Rules, and they decide whether this is usable at all:\n" +
		"- Every claim names the passage it comes from, quoted, at most eight words, in quotation marks.\n" +
		"- A claim you cannot tie to a passage or to a figure below: leave it out. Do not soften it.\n" +
		"- Describe what the author DOES, so that somebody can write that way — not how the texts feel.\n" +
		"- No praise, no summary of the content, no advice.\n\n")

	fmt.Fprintf(&sb, "## The author's passages (voice %q)\n\n", name)
	for _, ex := range b.Exemplars {
		fmt.Fprintf(&sb, "### %s\n%s\n\n", ex.Role, ex.Text)
	}

	sb.WriteString("## Measured over the whole corpus\n\n")
	for _, line := range measurementLines(b.Profile) {
		sb.WriteString("- " + line + "\n")
	}
	if len(b.Contrast) > 0 {
		sb.WriteString("\n## Where a model's text differs from this author\n\n" +
			"These are the strongest half of the description — the \"never\" section rests on them:\n\n")
		for _, c := range b.Contrast {
			fmt.Fprintf(&sb, "- %s: this author %.2f, a model %.2f\n", label(c), c.Author, c.Other)
		}
	}
	return sb.String()
}

// Card asks the organisation's model for the description. One call, no tools —
// the shape internal/llm exists for.
func Card(ctx context.Context, p llm.Provider, b Built, name string) (string, error) {
	if p == nil {
		return "", llm.ErrNoCredential
	}
	out, err := p.Complete(ctx, llm.Request{
		Tier:      llm.TierBest,
		MaxTokens: 2000,
		System: "You describe an author's style from measured evidence. You quote rather than " +
			"characterise, and you leave out what you cannot quote.",
		Messages: []llm.Message{{Role: "user", Content: CardPrompt(b, name)}},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// measurementLines turns the profile into the handful of figures a description
// can rest on — in words, with the band beside the value, and only the metrics
// that carry one.
func measurementLines(p style.Profile) []string {
	keys := make([]string, 0, len(p.Corpus))
	for k := range p.Corpus {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		name := style.Label[k]
		if name == "" {
			name = k
		}
		if band, ok := p.Bands[k]; ok {
			out = append(out, fmt.Sprintf("%s: %.2f (band %.2f to %.2f)", name, p.Corpus[k], band[0], band[1]))
			continue
		}
		out = append(out, fmt.Sprintf("%s: %.2f", name, p.Corpus[k]))
	}
	return out
}

func label(c Contrast) string {
	if c.Label != "" {
		return c.Label
	}
	return c.Metric
}
