package style

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// Measurement is what measure() returns: the counts and every metric by name.
// Metric names are the profile's band keys; see reference.md of the skill for
// what each one approximates and where it is blind.
type Measurement struct {
	Language   string             `json:"language"`
	Words      int                `json:"words"`
	Sentences  int                `json:"sentences"`
	Paragraphs int                `json:"paragraphs"`
	Headings   int                `json:"headings"`
	CodeBlocks int                `json:"code_blocks"`
	Values     map[string]float64 `json:"values"`
	// Evidence holds, for the list-based metrics, the phrases that were
	// counted and how often: metric → phrase → count. A finding that names
	// its phrases is revised; one that names a number is probed.
	Evidence map[string]map[string]int `json:"evidence,omitempty"`
}

func (m *Measurement) note(metric, phrase string) {
	if m.Evidence == nil {
		m.Evidence = map[string]map[string]int{}
	}
	if m.Evidence[metric] == nil {
		m.Evidence[metric] = map[string]int{}
	}
	m.Evidence[metric][strings.ToLower(strings.Join(strings.Fields(phrase), " "))]++
}

// ParagraphReport is one paragraph's detail for the findings.
type ParagraphReport struct {
	Index           int      `json:"index"`
	Words           int      `json:"words"`
	Sentences       int      `json:"sentences"`
	LongestSentence int      `json:"longest_sentence"`
	Anchors         []Anchor `json:"anchors"`
	Head            string   `json:"head"`
}

var (
	stretchRegex = compileAll(stretchVerbPatterns, "")
	hedgePhrases []*regexp.Regexp
	hedgeWords   = map[string]bool{}
	reDash       = regexp.MustCompile(`\s[–—-]\s|—`)
)

func init() {
	for _, h := range hedges {
		if strings.Contains(h, " ") {
			hedgePhrases = append(hedgePhrases, regexp.MustCompile(`\b`+regexp.QuoteMeta(h)+`\b`))
		} else {
			hedgeWords[h] = true
		}
	}
}

func round(x float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(x*p) / p
}

// per scales n/d, rounded to three decimals; 0 for an empty denominator.
func per(n, d, scale float64) float64 {
	if d == 0 {
		return 0
	}
	return round(n/d*scale, 3)
}

func fmean(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0
	for _, x := range xs {
		s += x
	}
	return float64(s) / float64(len(xs))
}

func median(xs []int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int{}, xs...)
	sort.Ints(s)
	n := len(s)
	if n%2 == 1 {
		return float64(s[n/2])
	}
	return float64(s[n/2-1]+s[n/2]) / 2
}

func pstdev(xs []int) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := fmean(xs)
	v := 0.0
	for _, x := range xs {
		d := float64(x) - m
		v += d * d
	}
	return math.Sqrt(v / float64(len(xs)))
}

// percentile with linear interpolation, as the reference does.
func percentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	xs := append([]float64{}, values...)
	sort.Float64s(xs)
	k := float64(len(xs)-1) * q
	f := math.Floor(k)
	c := math.Min(f+1, float64(len(xs)-1))
	return xs[int(f)] + (xs[int(c)]-xs[int(f)])*(k-f)
}

// Measure computes every metric of a text. lang forces the language; "" detects it.
var (
	antithesisTail = regexp.MustCompile(`,\s*(nicht|kein|keine|keinen|sondern|not|no|never|rather than),?\s([^.!?,;:]{1,60})[.!?]?\s*$`)
	antithesisTurn = regexp.MustCompile(`\b(nicht|not)\b[^.!?]{0,30},\s*(sondern|but)\b`)
	antithesisPair = regexp.MustCompile(`\b(kein|keine|keinen|no)\s\w+,\s*(kein|keine|keinen|no)\s\w+`)
)

// antithesisIn returns the antithesis a sentence closes on, or "" — the
// matched clause, for the finding.
func antithesisIn(sentence string) string {
	s := strings.ToLower(strings.TrimSpace(sentence))
	for _, re := range []*regexp.Regexp{antithesisTail, antithesisTurn, antithesisPair} {
		if hit := re.FindString(s); hit != "" {
			return strings.Join(strings.Fields(strings.Trim(hit, " ,.!?")), " ")
		}
	}
	return ""
}

func Measure(text, lang string) Measurement {
	prose, headings, codeBlocks := stripMarkdown(text, false)
	paragraphs := splitParagraphs(prose)
	var sentences []string
	for _, p := range paragraphs {
		sentences = append(sentences, splitSentences(p)...)
	}
	var words []string
	for _, s := range sentences {
		words = append(words, wordsOf(s)...)
	}
	if lang == "" {
		lang = detectLanguage(words)
	}
	m := Measurement{Language: lang, Words: len(words), Sentences: len(sentences),
		Paragraphs: len(paragraphs), Headings: len(headings), CodeBlocks: codeBlocks,
		Values: map[string]float64{}}
	nWords, nSent, nPara := float64(len(words)), float64(len(sentences)), float64(len(paragraphs))
	if nWords == 0 || nSent == 0 {
		return m
	}
	v := m.Values
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}

	// Sentence length
	lens := make([]int, len(sentences))
	for i, s := range sentences {
		lens[i] = len(wordsOf(s))
	}
	mean := fmean(lens)
	v["sent_len_mean"] = round(mean, 2)
	v["sent_len_median"] = median(lens)
	sd := pstdev(lens)
	v["sent_len_sd"] = round(sd, 2)
	if v["sent_len_mean"] != 0 {
		v["sent_len_cv"] = round(sd/v["sent_len_mean"], 3)
	}
	fl := make([]float64, len(lens))
	long, short, shortAfterLong := 0, 0, 0
	for i, l := range lens {
		fl[i] = float64(l)
		if l > 30 {
			long++
		}
		if l <= 8 {
			short++
		}
		if i > 0 && lens[i-1] > 25 && l <= 10 {
			shortAfterLong++
		}
	}
	v["sent_len_p90"] = percentile(fl, 0.9)
	v["long_sent_share"] = per(float64(long), nSent, 100)
	v["short_sent_share"] = per(float64(short), nSent, 100)
	v["short_after_long_rate"] = per(float64(shortAfterLong), math.Max(nSent-1, 1), 100)

	// Paragraph length
	plens := make([]int, len(paragraphs))
	longPara, maxPara := 0, 0
	for i, p := range paragraphs {
		plens[i] = len(wordsOf(p))
		if plens[i] > 90 {
			longPara++
		}
		if plens[i] > maxPara {
			maxPara = plens[i]
		}
	}
	v["para_len_mean"] = round(fmean(plens), 1)
	v["para_len_max"] = float64(maxPara)
	v["long_para_share"] = per(float64(longPara), nPara, 100)
	v["sentences_per_para"] = round(nSent/nPara, 2)
	// Structure: a text can hit every mean and still have no spine. Monotone
	// paragraph lengths, paragraphs of two sentences or fewer, and paragraphs
	// that open without a link to the one before are the measurable symptoms.
	if pm := fmean(plens); pm > 0 {
		v["para_len_cv"] = round(pstdev(plens)/pm, 3)
	} else {
		v["para_len_cv"] = 0
	}
	shortParas, linked := 0, 0
	for i, p := range paragraphs {
		if len(splitSentences(p)) <= 2 {
			shortParas++
		}
		if i > 0 {
			if ws := wordsOf(p); len(ws) > 0 && linkers[strings.ToLower(ws[0])] {
				linked++
			}
		}
	}
	v["short_para_share"] = per(float64(shortParas), nPara, 100)
	v["linked_para_share"] = per(float64(linked), math.Max(nPara-1, 1), 100)

	// Lexis: abstraction
	nominal, longWords, copulaHits, hedgeHits := 0, 0, 0, 0
	for i, w := range lower {
		if len([]rune(w)) >= 7 && hasAnySuffix(w, nominalSuffixes) {
			nominal++
		}
		if len([]rune(words[i])) >= 12 {
			longWords++
		}
		if copulas[w] {
			copulaHits++
		}
		if hedgeWords[w] {
			hedgeHits++
			m.note("hedge_rate", w)
		}
	}
	v["nominalisation_rate"] = per(float64(nominal), nWords, 100)
	v["long_word_rate"] = per(float64(longWords), nWords, 100)
	v["copula_per_sentence"] = per(float64(copulaHits), nSent, 1)
	proseLower := strings.ToLower(strings.Join(sentences, " "))
	stretch := 0
	for _, re := range stretchRegex {
		for _, hit := range re.FindAllString(proseLower, -1) {
			stretch++
			m.note("stretch_verb_rate", hit)
		}
	}
	v["stretch_verb_rate"] = per(float64(stretch), nWords, 1000)
	for _, re := range hedgePhrases {
		for _, hit := range re.FindAllString(proseLower, -1) {
			hedgeHits++
			m.note("hedge_rate", hit)
		}
	}
	v["hedge_rate"] = per(float64(hedgeHits), nWords, 1000)

	// Anchors
	nAnchors, withoutAnchor, quotes, examples := 0, 0, 0, 0
	for _, p := range paragraphs {
		as := findAnchors(p, lang)
		nAnchors += len(as)
		if len(as) == 0 {
			withoutAnchor++
		}
		for _, a := range as {
			switch a.Kind {
			case "quote":
				quotes++
			case "example":
				examples++
			}
		}
	}
	v["anchor_per_100_words"] = per(float64(nAnchors), nWords, 100)
	v["para_without_anchor_share"] = per(float64(withoutAnchor), nPara, 100)
	v["quote_rate"] = per(float64(quotes), nWords, 1000)
	v["example_rate"] = per(float64(examples), nWords, 1000)

	// Nesting
	sub := 0
	for _, w := range lower {
		if subordinators[w] {
			sub++
		}
	}
	commas := make([]int, len(sentences))
	maxCommas, deep := 0, 0
	for i, s := range sentences {
		commas[i] = strings.Count(s, ",")
		if commas[i] > maxCommas {
			maxCommas = commas[i]
		}
		if commas[i] >= 3 {
			deep++
		}
	}
	v["subordinators_per_sentence"] = per(float64(sub), nSent, 1)
	v["commas_per_sentence"] = round(fmean(commas), 2)
	v["max_commas_in_sentence"] = float64(maxCommas)
	v["deep_sent_share"] = per(float64(deep), nSent, 100)

	// Openers and rhythm
	var firsts []string
	for _, s := range sentences {
		if ws := wordsOf(s); len(ws) > 0 {
			firsts = append(firsts, strings.ToLower(ws[0]))
		}
	}
	repeat, pron, questions := 0, 0, 0
	for i, f := range firsts {
		if i > 0 && firsts[i-1] == f {
			repeat++
		}
		if pronounOpeners[f] {
			pron++
		}
	}
	for _, s := range sentences {
		if strings.HasSuffix(strings.TrimRight(s, " \t"), "?") {
			questions++
		}
	}
	v["opener_repeat_share"] = per(float64(repeat), math.Max(nSent-1, 1), 100)
	v["pronoun_opener_share"] = per(float64(pron), nSent, 100)
	v["question_rate"] = per(float64(questions), nSent, 100)
	// The antithesis closer: a sentence that ends on ", nicht Y" / ", kein Y" /
	// ", not Y", or turns on "nicht X, sondern Y" / "no X, no Y". People write
	// it once a page; a model closes every third paragraph with it.
	anti := 0
	for _, s := range sentences {
		if hit := antithesisIn(s); hit != "" {
			anti++
			m.note("antithesis_rate", hit)
		}
	}
	v["antithesis_rate"] = per(float64(anti), nWords, 1000)

	// Address
	counts := map[string]int{}
	for _, w := range lower {
		counts[w]++
	}
	sum := func(ks ...string) float64 {
		n := 0
		for _, k := range ks {
			n += counts[k]
		}
		return float64(n)
	}
	v["du_per_1000"] = per(sum("du", "dir", "dich", "dein", "deine", "you", "your"), nWords, 1000)
	sieFormal := 0
	for _, s := range sentences {
		ws := wordsOf(s)
		for _, w := range ws[min(1, len(ws)):] {
			switch w {
			case "Sie", "Ihnen", "Ihr", "Ihre", "Ihrem", "Ihren":
				sieFormal++
			}
		}
	}
	v["sie_per_1000"] = per(float64(sieFormal), nWords, 1000)
	v["wir_per_1000"] = per(sum("wir", "uns", "unser", "unsere", "we", "us", "our"), nWords, 1000)
	v["ich_per_1000"] = per(sum("ich", "mir", "mich", "mein", "meine", "i", "me", "my"), nWords, 1000)
	v["man_per_1000"] = per(sum("man"), nWords, 1000)

	// Punctuation
	joined := strings.Join(sentences, " ")
	v["colon_per_1000"] = per(float64(strings.Count(joined, ":")), nWords, 1000)
	v["semicolon_per_1000"] = per(float64(strings.Count(joined, ";")), nWords, 1000)
	v["paren_per_1000"] = per(float64(strings.Count(joined, "(")), nWords, 1000)
	v["exclam_per_1000"] = per(float64(strings.Count(joined, "!")), nWords, 1000)
	v["dash_per_1000"] = per(float64(len(reDash.FindAllStringIndex(joined, -1))), nWords, 1000)
	if len(headings) > 0 {
		withColon := 0
		for _, h := range headings {
			if strings.Contains(h, ":") {
				withColon++
			}
		}
		v["heading_colon_share"] = per(float64(withColon), float64(len(headings)), 100)
	} else {
		v["heading_colon_share"] = 0
	}
	return m
}

// WordCount is the number of words a text has for the purpose of the gate.
func WordCount(s string) int {
	return len(wordsOf(s))
}

func hasAnySuffix(w string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(w, s) {
			return true
		}
	}
	return false
}

// Paragraphs returns the per-paragraph detail the findings point at.
func Paragraphs(text, lang string) []ParagraphReport {
	prose, _, _ := stripMarkdown(text, false)
	paragraphs := splitParagraphs(prose)
	if lang == "" {
		var words []string
		for _, p := range paragraphs {
			words = append(words, wordsOf(p)...)
		}
		lang = detectLanguage(words)
	}
	out := make([]ParagraphReport, 0, len(paragraphs))
	for i, p := range paragraphs {
		sents := splitSentences(p)
		longest := 0
		for _, s := range sents {
			if n := len(wordsOf(s)); n > longest {
				longest = n
			}
		}
		ws := wordsOf(p)
		head := strings.Join(ws[:min(8, len(ws))], " ")
		out = append(out, ParagraphReport{Index: i + 1, Words: len(ws), Sentences: len(sents),
			LongestSentence: longest, Anchors: findAnchors(p, lang), Head: head})
	}
	return out
}

// Lexicon is the author's favourite content words per 10k and sentence openers.
type Lexicon struct {
	Favourites []LexiconEntry `json:"favourites"`
	Openers    []LexiconEntry `json:"openers"`
	Words      int            `json:"words"`
}

type LexiconEntry struct {
	Word   string  `json:"word"`
	Per10k float64 `json:"per_10k,omitempty"`
	Share  float64 `json:"share,omitempty"`
}

// BuildLexicon counts over several texts; top favourites, twelve openers.
func BuildLexicon(texts []string, top int) Lexicon {
	counts := map[string]int{}
	openers := map[string]int{}
	total, openerTotal := 0, 0
	for _, t := range texts {
		prose, _, _ := stripMarkdown(t, false)
		for _, p := range splitParagraphs(prose) {
			for _, s := range splitSentences(p) {
				ws := wordsOf(s)
				total += len(ws)
				if len(ws) > 0 {
					openers[ws[0]]++
					openerTotal++
				}
				for _, w := range ws {
					lw := strings.ToLower(w)
					if len([]rune(lw)) >= 4 && !stopwordsDE[lw] && !stopwordsEN[lw] {
						counts[lw]++
					}
				}
			}
		}
	}
	lex := Lexicon{Words: total}
	for _, e := range topN(counts, top) {
		lex.Favourites = append(lex.Favourites, LexiconEntry{Word: e.word, Per10k: round(float64(e.n)/float64(max(total, 1))*10000, 1)})
	}
	for _, e := range topN(openers, 12) {
		lex.Openers = append(lex.Openers, LexiconEntry{Word: e.word, Share: round(float64(e.n)/float64(max(openerTotal, 1))*100, 1)})
	}
	return lex
}

type wordCount struct {
	word string
	n    int
}

func topN(counts map[string]int, n int) []wordCount {
	out := make([]wordCount, 0, len(counts))
	for w, c := range counts {
		out = append(out, wordCount{w, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].word < out[j].word
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
