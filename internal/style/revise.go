package style

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The revision loop of the covey-style skill, in Go: measure, let a model
// revise the paragraphs the findings name, diff the claims, measure again.
// The loop stops when the score reaches zero, when an iteration does not
// improve on the best so far, or at MaxIter; the best version wins, not the
// last. The model is the caller's: a ModelCall can be the organisation's
// control-plane provider, a test double, anything that turns a system prompt
// and a user message into text.

// ModelCall turns a system prompt and a user message into the model's text.
type ModelCall func(ctx context.Context, system, user string) (string, error)

// ReviseInput is one revision job.
type ReviseInput struct {
	Text     string
	Material string   // facts the revision may add; everything else it may not
	Profile  *Profile // nil: readability defaults for the text's language
	Prose    string   // the profile's voice, appended to the editor rules
	MaxIter  int      // 0: 3
	Language string   // "": detected
}

// Iteration is one pass of the loop.
type Iteration struct {
	Number      int    `json:"number"`
	ScoreBefore int    `json:"score_before"`
	ScoreAfter  int    `json:"score_after"`
	Note        string `json:"note,omitempty"`
}

// ReviseResult is what the loop hands back.
type ReviseResult struct {
	Text       string      `json:"text"`
	Before     Report      `json:"before"`
	Best       Report      `json:"best"`
	Iterations []Iteration `json:"iterations"`
	StopReason string      `json:"stop_reason"`
}

const (
	defaultMaxIter = 3
	maxMaxIter     = 5
)

// DefaultProfile holds readability targets for a language, used when an agent
// has no profile of its own. Loose on purpose: they describe prose a reader
// gets through, not a particular author.
func DefaultProfile(lang string) Profile {
	bands := map[string][2]float64{
		"sent_len_mean":              {9.0, 18.0},
		"sent_len_cv":                {0.35, 1.5},
		"long_sent_share":            {0.0, 10.0},
		"long_para_share":            {0.0, 10.0},
		"nominalisation_rate":        {0.0, 4.0},
		"long_word_rate":             {0.0, 8.0},
		"copula_per_sentence":        {0.0, 0.7},
		"stretch_verb_rate":          {0.0, 2.0},
		"hedge_rate":                 {0.0, 3.0},
		"anchor_per_100_words":       {1.5, 100.0},
		"para_without_anchor_share":  {0.0, 25.0},
		"subordinators_per_sentence": {0.0, 0.5},
		"commas_per_sentence":        {0.0, 1.2},
		"deep_sent_share":            {0.0, 10.0},
		"opener_repeat_share":        {0.0, 15.0},
		"man_per_1000":               {0.0, 3.0},
	}
	if lang == "en" {
		bands["sent_len_mean"] = [2]float64{9.0, 20.0}
		bands["long_word_rate"] = [2]float64{0.0, 4.0}
		bands["copula_per_sentence"] = [2]float64{0.0, 0.8}
		bands["stretch_verb_rate"] = [2]float64{0.0, 3.0}
		bands["subordinators_per_sentence"] = [2]float64{0.0, 0.6}
		bands["commas_per_sentence"] = [2]float64{0.0, 1.3}
	}
	return Profile{Schema: Schema, Language: lang, Bands: bands}
}

// SystemPrompt is the editor rules plus the profile's voice.
func SystemPrompt(prose string) string {
	if strings.TrimSpace(prose) == "" {
		return EditorRules
	}
	return EditorRules + "\n\nThe text has to sound like this author. Voice, structure and anchors as described here; the exemplars show the manner, never wording to reuse:\n\n" + strings.TrimSpace(prose)
}

var reFence = regexp.MustCompile("(?s)\\A```[a-zA-Z]*\n(.*)\n```\\z")

// Unfence strips a code fence a model wrapped the text in despite the rules.
func Unfence(text string) string {
	t := strings.TrimSpace(text)
	if m := reFence.FindStringSubmatch(t); m != nil {
		t = m[1]
	}
	return t + "\n"
}

// Revise runs the loop.
func Revise(ctx context.Context, in ReviseInput, call ModelCall) (ReviseResult, error) {
	if strings.TrimSpace(in.Text) == "" {
		return ReviseResult{}, errors.New("no text to revise")
	}
	if call == nil {
		return ReviseResult{}, errors.New("no model to revise with")
	}
	maxIter := in.MaxIter
	if maxIter <= 0 {
		maxIter = defaultMaxIter
	}
	if maxIter > maxMaxIter {
		maxIter = maxMaxIter
	}
	lang := in.Language
	if lang == "" {
		lang = DetectLanguage(in.Text)
	}
	profile := in.Profile
	if profile == nil {
		p := DefaultProfile(lang)
		profile = &p
	}

	current := in.Text
	report := Check(current, profile)
	res := ReviseResult{Text: current, Before: report, Best: report}
	if report.Score == 0 {
		res.StopReason = "nothing to do"
		return res, nil
	}
	system := SystemPrompt(in.Prose)
	for n := 1; n <= maxIter; n++ {
		out, err := call(ctx, system, RevisionOrder(current, report, in.Material))
		if err != nil {
			return res, fmt.Errorf("iteration %d: %w", n, err)
		}
		next := Unfence(out)
		after := CheckRevision(next, current, in.Material, profile)
		it := Iteration{Number: n, ScoreBefore: report.Score, ScoreAfter: after.Score}
		if after.Score < res.Best.Score {
			res.Text, res.Best = next, after
		}
		if after.Score == 0 {
			res.Iterations = append(res.Iterations, it)
			res.StopReason = "no findings left"
			return res, nil
		}
		if after.Score >= report.Score {
			it.Note = "no improvement"
			res.Iterations = append(res.Iterations, it)
			res.StopReason = fmt.Sprintf("iteration %d did not improve (kept the best version)", n)
			return res, nil
		}
		res.Iterations = append(res.Iterations, it)
		current, report = next, after
	}
	res.StopReason = fmt.Sprintf("max_iter %d reached", maxIter)
	return res, nil
}
