package style

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Profile is the machine-readable half of a STYLE.md / TONE.md: the bands a
// draft should land in, the corpus values for orientation, the lexicon. The
// prose above the block (voice, structure, anchors, exemplars) is for the model
// and stays out of this struct.
type Profile struct {
	Schema    string                `json:"schema"`
	Language  string                `json:"language"`
	Documents int                   `json:"documents"`
	Words     int                   `json:"words"`
	Holdout   []string              `json:"holdout,omitempty"`
	Bands     map[string][2]float64 `json:"bands"`
	Corpus    map[string]float64    `json:"corpus,omitempty"`
	Lexicon   json.RawMessage       `json:"lexicon,omitempty"`
}

// ErrNoProfile: the Markdown carries no ```style-profile``` block.
var ErrNoProfile = errors.New("no style-profile block")

var reProfileBlock = regexp.MustCompile("(?s)```style-profile\\s*\n(.*?)\n```")

// ParseProfile reads the profile block out of a Markdown file and returns it
// together with the prose above the block.
func ParseProfile(markdown string) (Profile, string, error) {
	m := reProfileBlock.FindStringSubmatchIndex(markdown)
	if m == nil {
		return Profile{}, "", ErrNoProfile
	}
	var p Profile
	if err := json.Unmarshal([]byte(markdown[m[2]:m[3]]), &p); err != nil {
		return Profile{}, "", fmt.Errorf("style-profile block: %w", err)
	}
	if p.Schema != "" && p.Schema != Schema {
		return Profile{}, "", fmt.Errorf("style-profile schema %q, want %q", p.Schema, Schema)
	}
	return p, strings.TrimSpace(markdown[:m[0]]), nil
}

// StripProfileBlock removes the ```style-profile``` block from a Markdown
// file, for the prompt: the model reads the voice, the gate reads the numbers.
func StripProfileBlock(markdown string) string {
	return strings.TrimSpace(reProfileBlock.ReplaceAllString(markdown, ""))
}

// direction says which deviation from the corpus is a finding: "high" when a
// draft above the band is, "low" the opposite, "both" either way. Metrics not
// listed are informational.
var direction = map[string]string{
	"sent_len_mean": "both", "sent_len_cv": "low", "long_sent_share": "high",
	"short_sent_share": "both", "para_len_mean": "both", "long_para_share": "high",
	"sentences_per_para": "both", "nominalisation_rate": "high", "long_word_rate": "high",
	"copula_per_sentence": "high", "stretch_verb_rate": "high", "hedge_rate": "high",
	"anchor_per_100_words": "low", "para_without_anchor_share": "high", "example_rate": "low",
	"subordinators_per_sentence": "high", "commas_per_sentence": "high", "deep_sent_share": "high",
	"opener_repeat_share": "high", "pronoun_opener_share": "both", "question_rate": "both",
	"du_per_1000": "both", "sie_per_1000": "both", "wir_per_1000": "both", "ich_per_1000": "both",
	"man_per_1000": "high", "colon_per_1000": "both", "dash_per_1000": "high", "paren_per_1000": "both",
}

// Label is the human-readable name of a metric in findings.
var Label = map[string]string{
	"sent_len_mean":              "mean sentence length (words)",
	"sent_len_cv":                "sentence length variation (sd/mean)",
	"long_sent_share":            "sentences over 30 words (%)",
	"short_sent_share":           "sentences of 8 words or fewer (%)",
	"para_len_mean":              "mean paragraph length (words)",
	"long_para_share":            "paragraphs over 90 words (%)",
	"sentences_per_para":         "sentences per paragraph",
	"nominalisation_rate":        "nominalisations per 100 words",
	"long_word_rate":             "words of 12+ letters per 100 words",
	"copula_per_sentence":        "copulas per sentence",
	"stretch_verb_rate":          "light-verb constructions per 1000 words",
	"hedge_rate":                 "hedges and fillers per 1000 words",
	"anchor_per_100_words":       "anchors per 100 words",
	"para_without_anchor_share":  "paragraphs without an anchor (%)",
	"example_rate":               "example markers per 1000 words",
	"subordinators_per_sentence": "subordinators per sentence",
	"commas_per_sentence":        "commas per sentence",
	"deep_sent_share":            "sentences with 3+ commas (%)",
	"opener_repeat_share":        "consecutive sentences with the same first word (%)",
	"pronoun_opener_share":       "sentences opening with article or pronoun (%)",
	"question_rate":              "questions (%)",
	"du_per_1000":                "informal address per 1000 words",
	"sie_per_1000":               "formal address per 1000 words",
	"wir_per_1000":               "first person plural per 1000 words",
	"ich_per_1000":               "first person singular per 1000 words",
	"man_per_1000":               "impersonal 'man'/'one' per 1000 words",
	"colon_per_1000":             "colons per 1000 words",
	"dash_per_1000":              "dashes per 1000 words",
	"paren_per_1000":             "parentheses per 1000 words",
}

// minPad is the least a band may extend beyond the observed values on each
// side. Without it, a metric the author rarely uses gets a band a hair wide and
// every draft lands outside it at HIGH.
func minPad(key string, corpusValue float64) float64 {
	switch {
	case strings.HasSuffix(key, "_share") || key == "question_rate":
		return 3.0
	case strings.HasSuffix(key, "_per_1000") || strings.HasSuffix(key, "_rate"):
		return 1.0
	case key == "sent_len_cv":
		return 0.05
	case key == "sent_len_mean":
		return 1.5
	case key == "para_len_mean":
		return 10.0
	case strings.HasSuffix(key, "_per_sentence") || key == "sentences_per_para":
		return 0.15
	}
	return math.Max(math.Abs(corpusValue)*0.1, 0.05)
}

// Aggregate is the word-weighted corpus value of every metric.
func Aggregate(perDoc []Measurement) map[string]float64 {
	out := map[string]float64{}
	sums := map[string]float64{}
	weights := map[string]float64{}
	for _, d := range perDoc {
		for k, v := range d.Values {
			sums[k] += v * float64(d.Words)
			weights[k] += float64(d.Words)
		}
	}
	for k := range sums {
		if weights[k] > 0 {
			out[k] = round(sums[k]/weights[k], 3)
		}
	}
	return out
}

// BandsFrom derives the target bands. With four or more documents: the 20th to
// 80th percentile of the per-document values, widened by a tenth of that range
// and by minPad. With fewer: the observed minimum to maximum, padded by 15 % of
// the corpus value, which is honest about what was seen and no more.
func BandsFrom(perDoc []Measurement, corpus map[string]float64) map[string][2]float64 {
	bands := map[string][2]float64{}
	for key := range direction {
		var vals []float64
		for _, d := range perDoc {
			if v, ok := d.Values[key]; ok {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			continue
		}
		var lo, hi float64
		if len(vals) >= 4 {
			lo, hi = percentile(vals, 0.2), percentile(vals, 0.8)
			pad := math.Max((hi-lo)*0.1, minPad(key, corpus[key]))
			lo, hi = lo-pad, hi+pad
		} else {
			c, ok := corpus[key]
			if !ok {
				s := 0.0
				for _, v := range vals {
					s += v
				}
				c = s / float64(len(vals))
			}
			pad := math.Max(math.Abs(c)*0.15, minPad(key, c))
			mn, mx := vals[0], vals[0]
			for _, v := range vals {
				mn, mx = math.Min(mn, v), math.Max(mx, v)
			}
			lo, hi = mn-pad, mx+pad
		}
		bands[key] = [2]float64{round(math.Max(lo, 0), 3), round(hi, 3)}
	}
	return bands
}

// Finding is one metric outside its band.
type Finding struct {
	Metric   string     `json:"metric"`
	Label    string     `json:"label"`
	Value    float64    `json:"value"`
	Band     [2]float64 `json:"band"`
	Side     string     `json:"side"` // above | below
	Distance float64    `json:"distance"`
	Severity string     `json:"severity"` // HIGH | MEDIUM
}

// CheckAgainst compares a measurement with bands, in the direction that
// matters. Severity is the distance outside the band in units of its width.
func CheckAgainst(m Measurement, bands map[string][2]float64) []Finding {
	var out []Finding
	for key, band := range bands {
		v, ok := m.Values[key]
		if !ok {
			continue
		}
		lo, hi := band[0], band[1]
		dir := direction[key]
		if dir == "" {
			dir = "both"
		}
		width := math.Max(hi-lo, minPad(key, (lo+hi)/2))
		switch {
		case v > hi && (dir == "high" || dir == "both"):
			out = append(out, finding(key, v, band, "above", (v-hi)/width))
		case v < lo && (dir == "low" || dir == "both"):
			out = append(out, finding(key, v, band, "below", (lo-v)/width))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity == "HIGH"
		}
		if out[i].Distance != out[j].Distance {
			return out[i].Distance > out[j].Distance
		}
		return out[i].Metric < out[j].Metric
	})
	return out
}

func finding(key string, v float64, band [2]float64, side string, dist float64) Finding {
	sev := "MEDIUM"
	if dist >= 1.0 {
		sev = "HIGH"
	}
	return Finding{Metric: key, Label: Label[key], Value: v, Band: band, Side: side,
		Distance: round(dist, 2), Severity: sev}
}

// floors hold regardless of the author. Loose on purpose: the profile does the
// fine work, these catch a draft no reader gets through.
var floors = map[string]float64{
	"long_sent_share":           25.0,
	"para_without_anchor_share": 50.0,
	"nominalisation_rate":       8.0,
	"deep_sent_share":           25.0,
}

func floorFindings(m Measurement) []Finding {
	var out []Finding
	for _, key := range []string{"long_sent_share", "para_without_anchor_share", "nominalisation_rate", "deep_sent_share"} {
		limit := floors[key]
		if v, ok := m.Values[key]; ok && v > limit {
			out = append(out, Finding{Metric: key, Label: Label[key], Value: v, Band: [2]float64{0, limit},
				Side: "above", Distance: round((v-limit)/limit, 2), Severity: "HIGH"})
		}
	}
	return out
}

const (
	sentenceLimit  = 35
	paragraphLimit = 110
)

// ParagraphFinding names a place in the text the revision has to work on.
type ParagraphFinding struct {
	Paragraph int    `json:"paragraph"`
	Head      string `json:"head"`
	Kind      string `json:"kind"` // no_anchor | long_sentence | long_paragraph
	Text      string `json:"text"`
}

func paragraphFindings(text, lang string, profile *Profile) []ParagraphFinding {
	limit := paragraphLimit
	if profile != nil {
		if mean, ok := profile.Corpus["para_len_mean"]; ok && mean > 0 {
			limit = min(limit, int(mean*2.2))
		}
	}
	var out []ParagraphFinding
	for _, p := range Paragraphs(text, lang) {
		if len(p.Anchors) == 0 {
			out = append(out, ParagraphFinding{p.Index, p.Head, "no_anchor",
				"no number, name, example, quote or link: the paragraph asserts without holding on to anything"})
		}
		if p.LongestSentence > sentenceLimit {
			out = append(out, ParagraphFinding{p.Index, p.Head, "long_sentence",
				fmt.Sprintf("a sentence of %d words", p.LongestSentence)})
		}
		if p.Words > limit {
			out = append(out, ParagraphFinding{p.Index, p.Head, "long_paragraph",
				fmt.Sprintf("%d words in one paragraph (limit %d)", p.Words, limit)})
		}
	}
	return out
}

// Weights of the score: what a finding costs.
const (
	weightHigh      = 3
	weightMedium    = 1
	weightParagraph = 2
	weightMissing   = 3
	weightAdded     = 2
)

// Report is the outcome of a check: the measurement, the findings, the
// paragraphs to revise, and one number that has to fall.
type Report struct {
	Metrics    Measurement        `json:"metrics"`
	Findings   []Finding          `json:"findings"`
	Paragraphs []ParagraphFinding `json:"paragraphs"`
	Claims     *ClaimDiff         `json:"claims,omitempty"`
	Score      int                `json:"score"`
}

// Check measures a text against a profile (nil: readability floors only) and
// returns everything a revision needs.
func Check(text string, profile *Profile) Report {
	lang := ""
	if profile != nil {
		lang = profile.Language
	}
	m := Measure(text, lang)
	var findings []Finding
	if profile != nil && m.Words > 0 {
		findings = CheckAgainst(m, profile.Bands)
	}
	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.Metric] = true
	}
	for _, f := range floorFindings(m) {
		if !seen[f.Metric] {
			findings = append(findings, f)
		}
	}
	paras := paragraphFindings(text, m.Language, profile)
	r := Report{Metrics: m, Findings: findings, Paragraphs: paras}
	r.Score = score(r)
	return r
}

// CheckRevision is Check plus the claim diff against the previous version;
// anchors that appear in the material are not counted as invented.
func CheckRevision(text, previous, material string, profile *Profile) Report {
	r := Check(text, profile)
	d := Diff(previous, text)
	if material != "" {
		lower := strings.ToLower(material)
		kept := d.Added[:0]
		for _, a := range d.Added {
			if !strings.Contains(lower, strings.ToLower(a.Text)) {
				kept = append(kept, a)
			}
		}
		d.Added = kept
	}
	r.Claims = &d
	r.Score = score(r)
	return r
}

func score(r Report) int {
	s := 0
	for _, f := range r.Findings {
		if f.Severity == "HIGH" {
			s += weightHigh
		} else {
			s += weightMedium
		}
	}
	s += weightParagraph * len(r.Paragraphs)
	if r.Claims != nil {
		s += weightMissing*len(r.Claims.Missing) + weightAdded*len(r.Claims.Added)
	}
	return s
}

// FindingsText renders the findings the way a model receives them: metrics with
// their band, paragraphs by their first words, claims to restore or remove.
func FindingsText(r Report) string {
	var b strings.Builder
	if len(r.Findings) > 0 {
		b.WriteString("Whole text:\n")
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "- %s: %s is %s, target %s..%s\n", f.Severity, f.Label, num(f.Value), num(f.Band[0]), num(f.Band[1]))
		}
	}
	if len(r.Paragraphs) > 0 {
		b.WriteString("Paragraphs to revise (named by their first words):\n")
		for _, p := range r.Paragraphs {
			fmt.Fprintf(&b, "- \"%s…\": %s\n", p.Head, p.Text)
		}
	}
	if r.Claims != nil && (len(r.Claims.Missing) > 0 || len(r.Claims.Added) > 0) {
		b.WriteString("Claims:\n")
		for _, a := range r.Claims.Missing {
			fmt.Fprintf(&b, "- restore, it was in the previous version and is gone: %s %s\n", a.Kind, a.Text)
		}
		for _, a := range r.Claims.Added {
			fmt.Fprintf(&b, "- remove, it is neither in the text nor in the material: %s %s\n", a.Kind, a.Text)
		}
	}
	if b.Len() == 0 {
		return "No findings."
	}
	return strings.TrimRight(b.String(), "\n")
}

// RevisionOrder is the complete instruction for a model: the text, the
// findings, the material it may draw on.
func RevisionOrder(text string, r Report, material string) string {
	var b strings.Builder
	b.WriteString("<text>\n" + strings.TrimSpace(text) + "\n</text>\n\n<findings>\n" + FindingsText(r) + "\n</findings>")
	if strings.TrimSpace(material) != "" {
		b.WriteString("\n\n<material>\n" + strings.TrimSpace(material) + "\n</material>")
	}
	b.WriteString("\n\nRevise the named paragraphs so that the findings go away. Output the complete text.")
	return b.String()
}

// EditorRules is the system prompt of the revision: what a model may and may
// not do to a text. The profile's prose follows it.
const EditorRules = `You are an editor. You make a text easier to understand and more concrete without changing what it claims.

Rules, in order of precedence:
1. Every number, name, date, quotation, URL, code span and technical term of the text stays exactly as it is. You do not add a fact, figure or example that is not in the text or in the MATERIAL section. If a paragraph needs something concrete and the material has nothing, leave a one-line note in square brackets saying what is missing instead of inventing it.
2. Revise only the paragraphs named in the FINDINGS. Copy every other paragraph, heading, list and code block verbatim.
3. For a named paragraph, fix what the finding names: replace nominalisations and light-verb constructions with a verb and a subject that acts; cut hedges and fillers; split a sentence with several clauses into sentences with one idea each; bring in an anchor from the text or the material where the paragraph has none.
4. Keep the register and the form of address of the text. Keep its language. Do not make it chattier, do not add rhetorical questions or exclamation marks, do not summarise at the end.
5. Output the complete revised text and nothing else: no preamble, no explanation, no code fence around it. Preserve the Markdown structure.`

// Summary is the one-line count for logs and events.
func Summary(r Report) string {
	high, med := 0, 0
	for _, f := range r.Findings {
		if f.Severity == "HIGH" {
			high++
		} else {
			med++
		}
	}
	s := fmt.Sprintf("%d HIGH, %d MEDIUM, %d paragraphs", high, med, len(r.Paragraphs))
	if r.Claims != nil && (len(r.Claims.Missing) > 0 || len(r.Claims.Added) > 0) {
		s += fmt.Sprintf(", claims: %d missing, %d added", len(r.Claims.Missing), len(r.Claims.Added))
	}
	return s
}

func num(v float64) string {
	s := fmt.Sprintf("%.3f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}
