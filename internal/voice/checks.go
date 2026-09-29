package voice

import (
	"fmt"
	"sort"
	"strings"

	"covey/internal/style"
)

// The checks on a corpus while it is being collected (#458).
//
// The rules of a good corpus used to stand in the specification only — at
// least four texts long enough to carry a band, one register per voice — and
// a person found out that a build came out weak from its notes, after the
// fact. These say it while the texts go in, from the same measurements the
// build uses, and they say only what the measurements can tell: a text too
// short to carry a band, too few texts for a percentile spread, a text that
// addresses its reader differently from the rest, one in another language,
// one whose sentences are of a different kind. Whether a text is GOOD is not
// among them — nothing here can tell.

// reliableDocs is the number of texts from which the bands are a percentile
// spread rather than a padded min..max (Build).
const reliableDocs = 4

// registerMinWords is the length from which a text's address is read at all.
// Below it one "Sie" in a sentence would decide the register.
const registerMinWords = 60

// Check is one finding about a corpus. Code is for the interface to word it;
// Message is the same in English, for the API and the log.
type Check struct {
	Code  string `json:"code"`
	Level string `json:"level"` // ok | warn | block
	// N is a count where the check has one: how many texts are missing.
	N int `json:"n,omitempty"`
	// Docs names the texts a check is about.
	Docs []string `json:"docs,omitempty"`
	// Detail qualifies the finding: the register or the language the texts
	// differ in.
	Detail  string `json:"detail,omitempty"`
	Message string `json:"message"`
}

// The codes.
const (
	CheckNoTexts        = "no_texts"
	CheckMoreTexts      = "more_texts"
	CheckShortTexts     = "short_texts"
	CheckRegister       = "register"
	CheckLanguage       = "language"
	CheckSentenceLength = "sentence_length"
	CheckEnough         = "enough"
)

// The levels. Only a missing corpus blocks: a build on three texts is usable
// and says what it rests on.
const (
	LevelOK    = "ok"
	LevelWarn  = "warn"
	LevelBlock = "block"
)

// CheckCorpus says what a corpus of author texts lacks. lang is the voice's
// language, empty to take the corpus's majority.
func CheckCorpus(docs []Document, lang string) []Check {
	if len(docs) == 0 {
		return []Check{{Code: CheckNoTexts, Level: LevelBlock,
			Message: "no texts yet — a measured voice is built from at least one"}}
	}
	type measured struct {
		name string
		m    style.Measurement
	}
	var all []measured
	langs := map[string]int{}
	var short []string
	usable := 0
	for _, d := range docs {
		m := style.Measure(d.Text, "")
		all = append(all, measured{d.Name, m})
		langs[m.Language]++
		if m.Words >= usableWords {
			usable++
		} else {
			short = append(short, d.Name)
		}
	}
	var out []Check
	if usable < reliableDocs {
		out = append(out, Check{Code: CheckMoreTexts, Level: LevelWarn, N: reliableDocs - usable,
			Message: fmt.Sprintf("%d more texts of %d words or more for reliable bands", reliableDocs-usable, usableWords)})
	}
	if len(short) > 0 {
		out = append(out, Check{Code: CheckShortTexts, Level: LevelWarn, Docs: short,
			Message: fmt.Sprintf("fewer than %d words — not used for bands: %s", usableWords, strings.Join(short, ", "))})
	}

	// Language: a text in another language than the voice's.
	want := strings.TrimSpace(lang)
	if want == "" {
		want = majority(langs)
	}
	var foreign []string
	for _, d := range all {
		if d.m.Words >= registerMinWords && d.m.Language != "" && d.m.Language != want {
			foreign = append(foreign, d.name)
		}
	}
	if len(foreign) > 0 {
		out = append(out, Check{Code: CheckLanguage, Level: LevelWarn, Docs: foreign, Detail: want,
			Message: fmt.Sprintf("not in %s — one voice per language: %s", want, strings.Join(foreign, ", "))})
	}

	// Register, read off the address: a text that says "Sie" to its reader and
	// one that says "du" are written for different occasions, and their bands
	// fit neither. A text that addresses nobody is neutral and fits both.
	registers := map[string][]string{}
	for _, d := range all {
		if d.m.Words < registerMinWords {
			continue
		}
		if r := addressOf(d.m); r != "" {
			registers[r] = append(registers[r], d.name)
		}
	}
	if len(registers) > 1 {
		// The minority is the finding; on a tie, the formal side is named, so
		// the answer does not depend on map order.
		names := make([]string, 0, len(registers))
		for r := range registers {
			names = append(names, r)
		}
		sort.Slice(names, func(i, j int) bool {
			if len(registers[names[i]]) != len(registers[names[j]]) {
				return len(registers[names[i]]) < len(registers[names[j]])
			}
			return names[i] > names[j]
		})
		odd := names[0]
		out = append(out, Check{Code: CheckRegister, Level: LevelWarn, Docs: registers[odd], Detail: odd,
			Message: fmt.Sprintf("these texts look like a different register (%s address): %s",
				odd, strings.Join(registers[odd], ", "))})
	}

	// Sentence shape: a text whose sentences run far longer or shorter than
	// the others' — a legal notice among blog posts, a list of one-liners.
	// Needs three measured texts; with two, neither is "the rest".
	if len(all) >= 3 {
		var odd []string
		for i, d := range all {
			mine := d.m.Values["sent_len_mean"]
			var rest []float64
			for j, o := range all {
				if j != i && o.m.Values["sent_len_mean"] > 0 {
					rest = append(rest, o.m.Values["sent_len_mean"])
				}
			}
			med := medianOf(rest)
			if mine == 0 || med == 0 {
				continue
			}
			if ratio := mine / med; ratio > sentenceOutlier || ratio < 1/sentenceOutlier {
				odd = append(odd, d.name)
			}
		}
		if len(odd) > 0 {
			out = append(out, Check{Code: CheckSentenceLength, Level: LevelWarn, Docs: odd,
				Message: "sentences much longer or shorter than in the other texts: " + strings.Join(odd, ", ")})
		}
	}
	if len(out) == 0 {
		out = append(out, Check{Code: CheckEnough, Level: LevelOK,
			Message: fmt.Sprintf("%d texts of one register — enough for percentile bands", usable)})
	}
	return out
}

// sentenceOutlier is how far a text's mean sentence length may be from the
// others' median before it is named: 60 % longer, or the inverse.
const sentenceOutlier = 1.6

// addressOf is the register a text's address suggests: "formal" for Sie,
// "informal" for du, "" when it addresses nobody or both. English "you" counts
// as informal — it is the text speaking to its reader either way, and the
// check only compares texts of one voice with each other.
func addressOf(m style.Measurement) string {
	du, sie := m.Values["du_per_1000"], m.Values["sie_per_1000"]
	switch {
	case sie >= 4 && du < 1:
		return "formal"
	case du >= 4 && sie < 1:
		return "informal"
	}
	return ""
}

func medianOf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
