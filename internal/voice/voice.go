// Package voice builds an author's style into an object an agent can carry
// (spec/24-voice.md).
//
// The style gate holds a text inside bands measured from a corpus
// (internal/style, spec/06). The bands say how FAR a text is from the corpus;
// they cannot make it sound like the corpus's author. Measured on covey.work:
// the writing agents satisfied "sentences of 13 to 20 words, no dashes" and
// still read as nobody — too few anchors, paragraphs all of one shape, an
// antithesis at the end of every third one.
//
// What transfers a voice is three things at once: examples of it in the prompt,
// a description in words of what the author does and never does, and the
// profile as the guard behind both. This package builds all three from a folder
// of texts:
//
//   - the PROFILE, as the gate already understands it (internal/style),
//   - the EXEMPLARS, five to eight paragraphs chosen for variety,
//   - the CONTRAST, what the author never does, measured against a reference,
//   - the CARD, a description in words — one model call, in card.go.
//
// Everything but the card is deterministic: the same corpus gives the same
// profile, the same exemplars and the same contrast, so a rebuild after a
// metric change is safe and a card a person has released survives it.
package voice

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"covey/internal/style"
)

// minExemplarWords / maxExemplarWords bound what may be an exemplar. Too short
// and it shows no hand; too long and it fills the prompt of every run of every
// agent carrying this voice — which is the price of the feature and is paid per
// run, not per build.
const (
	minExemplarWords = 25
	maxExemplarWords = 180
	// The spec's five to eight. Fewer than five shows too little variety, more
	// than eight buys none.
	minExemplars = 5
	maxExemplars = 8
	// usableWords is the length below which a document does not carry a band.
	// The same figure the CLI's profile build uses.
	usableWords = 150
)

// addressMetrics are measured and not banded — see the rule in Build.
var addressMetrics = []string{
	"du_per_1000", "sie_per_1000", "wir_per_1000", "ich_per_1000", "man_per_1000",
	"question_rate",
}

// Document is one text of the corpus.
type Document struct {
	Name string
	Text string
}

// Exemplar is one passage of the corpus, with the reason it was chosen. The
// reason is not decoration: it is what makes the set checkable by a person —
// six paragraphs that are all "evidence" are not variety.
type Exemplar struct {
	Role string `json:"role"`
	Text string `json:"text"`
	From string `json:"from,omitempty"`
}

// Roles, in the order they are looked for. An opening and a closing carry the
// author's hand most visibly; the two length extremes are what a band cannot
// express at all.
const (
	RoleOpening  = "opening"
	RoleEvidence = "evidence"
	RoleExample  = "example"
	RoleLong     = "long"
	RoleShort    = "short"
	RoleClosing  = "closing"
)

// Contrast is one metric on which a reference corpus of AI text sits outside
// the author's band, in words. "Never" is the sharpest signal a corpus yields,
// and it falls out of the comparison rather than being written by anybody.
type Contrast struct {
	Metric string  `json:"metric"`
	Label  string  `json:"label"`
	Author float64 `json:"author"`
	Other  float64 `json:"other"`
}

// Built is one pass of the build.
type Built struct {
	Profile   style.Profile `json:"profile"`
	Exemplars []Exemplar    `json:"exemplars"`
	Contrast  []Contrast    `json:"contrast,omitempty"`
	// Notes are what the build wants a person to know about its own result: too
	// few documents, one register too many, no reference to contrast against.
	// They are shown at the build, not buried in a log — a profile built on
	// three documents is usable and has to say so.
	Notes []string `json:"notes,omitempty"`
	Words int      `json:"words"`
	Docs  int      `json:"documents"`
}

// Build measures a corpus and derives profile, exemplars and contrast.
//
// lang forces the language; empty detects it from the corpus. reference is the
// measured AI corpus the contrast is drawn against — nil means no contrast,
// which is a state and not a failure: covey ships no such corpus, because a
// reference nobody measured is a number invented with a straight face, and the
// organisation that has AI drafts of its own has the honest one.
func Build(docs []Document, lang string, reference map[string]float64) (Built, error) {
	if len(docs) == 0 {
		return Built{}, fmt.Errorf("a voice needs at least one text")
	}
	var out Built
	var perDoc, usable []style.Measurement
	var texts []string
	langs := map[string]int{}
	for _, d := range docs {
		m := style.Measure(d.Text, lang)
		perDoc = append(perDoc, m)
		texts = append(texts, d.Text)
		out.Words += m.Words
		langs[m.Language]++
		if m.Words >= usableWords {
			usable = append(usable, m)
		}
	}
	if lang == "" {
		lang = majority(langs)
	}
	corpus := style.Aggregate(perDoc)
	lexJSON, err := json.Marshal(style.BuildLexicon(texts, 40))
	if err != nil {
		return Built{}, fmt.Errorf("lexicon: %w", err)
	}
	// Bands come from the documents long enough to carry one. If NONE is, they
	// come from all of them anyway: a voice whose profile has no bands measures
	// nothing, and a gate that measures nothing is the silence this whole
	// document exists to remove. The note below says which of the two happened.
	banding := usable
	if len(banding) == 0 {
		banding = perDoc
	}
	out.Docs = len(usable)
	out.Profile = style.Profile{
		Schema:    style.Schema,
		Language:  lang,
		Documents: len(usable),
		Words:     out.Words,
		Bands:     style.BandsFrom(banding, corpus),
		Corpus:    style.BandValues(corpus),
		Lexicon:   lexJSON,
	}
	// The address metrics leave the bands. How often a text says "Sie" or "wir"
	// belongs to the REGISTER it was written in, not to the author's hand: the
	// same person says "Sie" in a tender and "wir" in a blog post, and a band
	// built from one corpus would report the other as a finding on every
	// paragraph. They stay in the corpus figures, where they are orientation
	// rather than a rule (spec/24).
	for _, metric := range addressMetrics {
		delete(out.Profile.Bands, metric)
	}

	switch {
	case len(usable) == 0:
		out.Notes = append(out.Notes, fmt.Sprintf(
			"none of the %d texts reaches %d words, so every one of them carries a band: the result is "+
				"the observed min..max padded by 15 %%, and it rests on very little", len(docs), usableWords))
	case len(usable) < 4:
		out.Notes = append(out.Notes, fmt.Sprintf(
			"%d of %d texts are long enough to carry a band (%d words); the bands are the observed "+
				"min..max padded by 15 %%, not a percentile spread", len(usable), len(docs), usableWords))
	}
	if len(langs) > 1 {
		out.Notes = append(out.Notes, "the corpus holds more than one language — one voice per language, "+
			"otherwise the bands fit neither")
	}
	out.Exemplars = pickExemplars(docs, lang)
	if len(out.Exemplars) < minExemplars {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"only %d passages were usable as exemplars (wanted %d): too short, too long, or closing on an "+
				"antithesis", len(out.Exemplars), minExemplars))
	}
	if reference == nil {
		out.Notes = append(out.Notes, "no reference corpus — without one there is no contrast list, "+
			"and \"what the author never does\" is the sharpest half of a voice")
	} else {
		out.Contrast = contrastWith(out.Profile, reference)
	}
	return out, nil
}

// exemplarCandidate is one paragraph the build may show as an example, with
// what is known about it. Kept at package scope rather than inside the picker
// so the ordering helpers can be read on their own.
type exemplarCandidate struct {
	text    string
	from    string
	words   int
	anchors int
	example bool
	first   bool
	last    bool
}

// pickExemplars chooses the passages that go into the prompt.
//
// One per role, in role order, and every paragraph only once — the point is
// variety, and six paragraphs that all carry evidence show a model one move.
// Ties are broken by the order in the corpus, so the build stays deterministic.
func pickExemplars(docs []Document, lang string) []Exemplar {
	all := exemplarCandidates(docs, lang)
	if len(all) == 0 {
		return nil
	}
	longest := append([]exemplarCandidate(nil), all...)
	sort.SliceStable(longest, func(i, j int) bool { return longest[i].words > longest[j].words })
	shortest := append([]exemplarCandidate(nil), all...)
	sort.SliceStable(shortest, func(i, j int) bool { return shortest[i].words < shortest[j].words })
	richest := append([]exemplarCandidate(nil), all...)
	sort.SliceStable(richest, func(i, j int) bool { return richest[i].anchors > richest[j].anchors })

	picked := map[string]bool{}
	var out []Exemplar
	take := func(role string, list []exemplarCandidate, pred func(exemplarCandidate) bool) {
		if len(out) >= maxExemplars {
			return
		}
		for _, c := range list {
			if picked[c.text] || !pred(c) {
				continue
			}
			picked[c.text] = true
			out = append(out, Exemplar{Role: role, Text: c.text, From: c.from})
			return
		}
	}
	any := func(exemplarCandidate) bool { return true }

	take(RoleOpening, all, func(c exemplarCandidate) bool { return c.first })
	take(RoleEvidence, all, func(c exemplarCandidate) bool { return c.anchors >= 2 })
	take(RoleExample, all, func(c exemplarCandidate) bool { return c.example })
	take(RoleLong, longest, any)
	take(RoleShort, shortest, any)
	take(RoleClosing, all, func(c exemplarCandidate) bool { return c.last })
	// Fill up to the minimum with whatever carries the most anchors: three
	// passages show too little variety, and the next best one is the concrete
	// one.
	for len(out) < minExemplars {
		before := len(out)
		take(RoleEvidence, richest, any)
		if len(out) == before {
			break // nothing left
		}
	}
	return out
}

// exemplarCandidates is the filter: what may be an exemplar at all.
func exemplarCandidates(docs []Document, lang string) []exemplarCandidate {
	var all []exemplarCandidate
	seen := map[string]bool{}
	for _, d := range docs {
		paras := style.ParagraphTexts(d.Text)
		for i, p := range paras {
			p = strings.TrimSpace(p)
			key := strings.Join(strings.Fields(strings.ToLower(p)), " ")
			if key == "" || seen[key] {
				// The same paragraph in two documents counts once: a repeated
				// closing is not an example of anything.
				continue
			}
			n := len(strings.Fields(p))
			if n < minExemplarWords || n > maxExemplarWords {
				continue
			}
			if !style.IsProse(p) || style.ClosesOnAntithesis(p) {
				continue
			}
			seen[key] = true
			c := exemplarCandidate{text: p, from: d.Name, words: n,
				first: i == 0, last: i == len(paras)-1}
			for _, a := range style.Anchors(p, lang) {
				c.anchors++
				if a.Kind == "example" {
					c.example = true
				}
			}
			all = append(all, c)
		}
	}
	return all
}

// contrastWith names the metrics on which the reference sits outside the
// author's band — in both directions, because "never does" and "always does"
// are the same finding seen from two sides.
func contrastWith(p style.Profile, reference map[string]float64) []Contrast {
	var out []Contrast
	for metric, band := range p.Bands {
		other, ok := reference[metric]
		if !ok {
			continue
		}
		lo, hi := band[0], band[1]
		width := hi - lo
		if width <= 0 {
			width = hi
		}
		if other > hi+width || other < lo-width {
			author := p.Corpus[metric]
			out = append(out, Contrast{Metric: metric, Label: style.Label[metric], Author: author, Other: other})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metric < out[j].Metric })
	return out
}

func majority(counts map[string]int) string {
	best, n := "", -1
	for lang, c := range counts {
		if c > n || (c == n && lang < best) {
			best, n = lang, c
		}
	}
	return best
}

// Reference measures the other pole: the corpus of AI text the contrast list is
// drawn against. Nil for an empty one, which is what Build reads as "no
// contrast" — and that is a state to report, not an error to raise.
func Reference(docs []Document) map[string]float64 {
	if len(docs) == 0 {
		return nil
	}
	var ms []style.Measurement
	for _, d := range docs {
		ms = append(ms, style.Measure(d.Text, ""))
	}
	return style.Aggregate(ms)
}

// The threshold below which a change is not a correction.
//
// A voice learns from a pair, and a pair that differs by a comma teaches
// nothing while costing room in every card prompt. The rule is two conditions,
// both cheap to explain — which matters, because an agent gets this sentence
// back as a refusal and has to be able to act on it:
//
//   - the corrected text has to be long enough to have a style at all,
//   - and enough words have to have moved.
//
// Word-based rather than character-based on purpose: a typo fixed in a long
// paragraph moves one word, and that is exactly the case this rejects.
const (
	minCorrectionWords = 20
	// The hurdle is the LARGER of the two, and both are needed: three changed
	// words in a short passage are a real edit, the same three in a page are a
	// typo hunt. An absolute floor alone would let the page through; a share
	// alone would let a single fixed word through in a short one.
	minChangedWords = 3
	minChangedShare = 0.05
)

// IsCorrection reports whether the step from before to after is worth keeping,
// and says why not when it is not. The reason goes back to whoever offered the
// pair, so it is written to be read by them.
func IsCorrection(before, after string) (bool, string) {
	before, after = strings.TrimSpace(before), strings.TrimSpace(after)
	if before == "" || after == "" {
		return false, "a pair needs both halves"
	}
	if before == after {
		return false, "the two halves are the same text"
	}
	words := strings.Fields(after)
	if len(words) < minCorrectionWords {
		return false, fmt.Sprintf("the corrected text has %d words; below %d there is no style to learn from",
			len(words), minCorrectionWords)
	}
	changed := changedWords(before, after)
	longer := max(len(strings.Fields(before)), len(words))
	needed := max(minChangedWords, int(minChangedShare*float64(longer)))
	if changed < needed {
		return false, fmt.Sprintf("%d of %d words differ, %d would be needed — that is a typo, not a correction",
			changed, longer, needed)
	}
	return true, ""
}

// changedWords counts how many words the two texts do not have in common, as a
// multiset: a moved sentence counts as unchanged, a rewritten one as changed.
// Cheaper than an edit distance and closer to the question — what a voice
// learns from is different WORDING, not a different order.
func changedWords(before, after string) int {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, w := range strings.Fields(strings.ToLower(s)) {
			m[strings.Trim(w, ".,;:!?\"'()[]„“”»«")]++
		}
		return m
	}
	a, b := count(before), count(after)
	diff := 0
	for w, n := range b {
		if n > a[w] {
			diff += n - a[w]
		}
	}
	for w, n := range a {
		if n > b[w] {
			diff += n - b[w]
		}
	}
	return diff
}
