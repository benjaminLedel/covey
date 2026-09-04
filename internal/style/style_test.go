package style

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"testing"
)

// The fixtures were produced by the covey-style skill's Python implementation
// (scripts/textmetrics.py, claim_diff.py) over the same texts. The Go port has
// to agree with them; where it cannot, the difference is documented here and
// not papered over.
type fixtures struct {
	Texts map[string]struct {
		Text       string             `json:"text"`
		Metrics    map[string]any     `json:"metrics"`
		Paragraphs []fixtureParagraph `json:"paragraphs"`
	}
	ClaimDiff struct {
		Before, After string
		Diff          struct {
			Before, After  int
			Missing, Added []ClaimAnchor
		}
	} `json:"claim_diff"`
	BandsThin map[string][2]float64 `json:"bands_thin"`
}

type fixtureParagraph struct {
	Index           int        `json:"index"`
	Words           int        `json:"words"`
	Sentences       int        `json:"sentences"`
	LongestSentence int        `json:"longest_sentence"`
	Anchors         [][]string `json:"anchors"`
	Head            string     `json:"head"`
}

func loadFixtures(t *testing.T) fixtures {
	t.Helper()
	raw, err := os.ReadFile("testdata/python_metrics.json")
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	var fx fixtures
	fx.Texts = map[string]struct {
		Text       string             `json:"text"`
		Metrics    map[string]any     `json:"metrics"`
		Paragraphs []fixtureParagraph `json:"paragraphs"`
	}{}
	for k, v := range all {
		switch k {
		case "claim_diff":
			if err := json.Unmarshal(v, &fx.ClaimDiff); err != nil {
				t.Fatal(err)
			}
		case "bands_thin":
			if err := json.Unmarshal(v, &fx.BandsThin); err != nil {
				t.Fatal(err)
			}
		default:
			var e struct {
				Text       string             `json:"text"`
				Metrics    map[string]any     `json:"metrics"`
				Paragraphs []fixtureParagraph `json:"paragraphs"`
			}
			if err := json.Unmarshal(v, &e); err != nil {
				t.Fatal(err)
			}
			fx.Texts[k] = e
		}
	}
	return fx
}

func TestParityWithPython(t *testing.T) {
	fx := loadFixtures(t)
	names := make([]string, 0, len(fx.Texts))
	for n := range fx.Texts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		f := fx.Texts[name]
		t.Run(name, func(t *testing.T) {
			m := Measure(f.Text, "")
			if got, want := m.Language, f.Metrics["language"]; got != want {
				t.Errorf("language: got %v want %v", got, want)
			}
			counts := map[string]int{"words": m.Words, "sentences": m.Sentences, "paragraphs": m.Paragraphs,
				"headings": m.Headings, "code_blocks": m.CodeBlocks}
			for k, want := range f.Metrics {
				if k == "language" {
					continue
				}
				w := want.(float64)
				if got, ok := counts[k]; ok {
					if float64(got) != w {
						t.Errorf("%s: got %d want %v", k, got, w)
					}
					continue
				}
				got, ok := m.Values[k]
				if !ok {
					t.Errorf("%s: missing", k)
					continue
				}
				if math.Abs(got-w) > 0.0015 {
					t.Errorf("%s: got %v want %v", k, got, w)
				}
			}
			paras := Paragraphs(f.Text, "")
			if len(paras) != len(f.Paragraphs) {
				t.Fatalf("paragraphs: got %d want %d", len(paras), len(f.Paragraphs))
			}
			for i, p := range paras {
				want := f.Paragraphs[i]
				if p.Words != want.Words || p.Sentences != want.Sentences || p.LongestSentence != want.LongestSentence || p.Head != want.Head {
					t.Errorf("paragraph %d: got %+v want %+v", i+1, p, want)
				}
				if len(p.Anchors) != len(want.Anchors) {
					t.Errorf("paragraph %d anchors: got %v want %v", i+1, p.Anchors, want.Anchors)
					continue
				}
				for j, a := range p.Anchors {
					if a.Kind != want.Anchors[j][0] || a.Text != want.Anchors[j][1] {
						t.Errorf("paragraph %d anchor %d: got %v want %v", i+1, j, a, want.Anchors[j])
					}
				}
			}
		})
	}
}

func TestClaimDiffParity(t *testing.T) {
	fx := loadFixtures(t)
	d := Diff(fx.ClaimDiff.Before, fx.ClaimDiff.After)
	want := fx.ClaimDiff.Diff
	if d.Before != want.Before || d.After != want.After {
		t.Errorf("counts: got %d/%d want %d/%d", d.Before, d.After, want.Before, want.After)
	}
	if got, w := jsonOf(d.Missing), jsonOf(want.Missing); got != w {
		t.Errorf("missing: got %s want %s", got, w)
	}
	if got, w := jsonOf(d.Added), jsonOf(want.Added); got != w {
		t.Errorf("added: got %s want %s", got, w)
	}
}

func TestBandsParity(t *testing.T) {
	fx := loadFixtures(t)
	concrete := fx.Texts["concrete_de"].Text
	doc := Measure(concrete+"\n\n"+concrete, "")
	docs := []Measurement{doc, doc, doc}
	bands := BandsFrom(docs, Aggregate(docs))
	for k, want := range fx.BandsThin {
		got, ok := bands[k]
		if !ok {
			t.Errorf("%s: missing", k)
			continue
		}
		if math.Abs(got[0]-want[0]) > 0.0015 || math.Abs(got[1]-want[1]) > 0.0015 {
			t.Errorf("%s: got %v want %v", k, got, want)
		}
	}
}

func TestCheckSeparatesGenericFromConcrete(t *testing.T) {
	fx := loadFixtures(t)
	concrete := fx.Texts["concrete_de"].Text
	doc := Measure(concrete+"\n\n"+concrete, "")
	docs := []Measurement{doc, doc, doc}
	p := &Profile{Schema: Schema, Language: "de", Documents: 3, Bands: BandsFrom(docs, Aggregate(docs)), Corpus: Aggregate(docs)}

	generic := Check(fx.Texts["generic_de"].Text, p)
	if generic.Score == 0 {
		t.Fatal("the generic text passed")
	}
	metrics := map[string]bool{}
	for _, f := range generic.Findings {
		if f.Severity == "HIGH" {
			metrics[f.Metric] = true
		}
	}
	for _, want := range []string{"nominalisation_rate", "para_without_anchor_share", "hedge_rate"} {
		if !metrics[want] {
			t.Errorf("expected a HIGH finding on %s, got %v", want, generic.Findings)
		}
	}
	if len(generic.Paragraphs) != 3 {
		t.Errorf("expected 3 anchorless paragraphs, got %v", generic.Paragraphs)
	}
	own := Check(concrete, p)
	for _, f := range own.Findings {
		if f.Severity == "HIGH" {
			t.Errorf("the author's own text has a HIGH finding: %+v", f)
		}
	}
	order := RevisionOrder(fx.Texts["generic_de"].Text, generic, "")
	for _, want := range []string{"<text>", "<findings>", "Paragraphs to revise", "Die Digitalisierung stellt"} {
		if !contains(order, want) {
			t.Errorf("revision order lacks %q", want)
		}
	}
}

func TestFloorsAndAnchorPointersYieldToTheProfile(t *testing.T) {
	fx := loadFixtures(t)
	generic := fx.Texts["generic_de"].Text // 100 % anchorless paragraphs, 13 nominalisations per 100
	// A house that writes most paragraphs without an anchor: the floor (50 %)
	// must not overrule the band, and no paragraph is pointed at.
	house := &Profile{Language: "de", Bands: map[string][2]float64{
		"para_without_anchor_share": {57, 100}, "nominalisation_rate": {0, 20}}}
	r := Check(generic, house)
	for _, f := range r.Findings {
		if f.Metric == "para_without_anchor_share" || f.Metric == "nominalisation_rate" {
			t.Errorf("the band is the author's word, got %+v", f)
		}
	}
	for _, p := range r.Paragraphs {
		if p.Kind == "no_anchor" {
			t.Fatalf("no anchor pointer when the profile allows anchorless paragraphs: %+v", p)
		}
	}
	// Without a band the floor holds, and the pointers say where.
	r = Check(generic, &Profile{Language: "de", Bands: map[string][2]float64{"sent_len_mean": {5, 40}}})
	found := false
	for _, f := range r.Findings {
		if f.Metric == "para_without_anchor_share" && f.Severity == "HIGH" {
			found = true
		}
	}
	if !found || len(r.Paragraphs) != 3 {
		t.Fatalf("floor and pointers expected without a band: %+v / %d pointers", r.Findings, len(r.Paragraphs))
	}
	// A band that the text exceeds: pointers name the paragraphs.
	r = Check(generic, &Profile{Language: "de", Bands: map[string][2]float64{"para_without_anchor_share": {0, 25}}})
	if len(r.Paragraphs) != 3 {
		t.Fatalf("pointers expected when the share is outside the band, got %d", len(r.Paragraphs))
	}
}

func TestFindingsNameTheirPhrases(t *testing.T) {
	fx := loadFixtures(t)
	generic := fx.Texts["generic_de"].Text
	r := Check(generic, &Profile{Language: "de", Bands: map[string][2]float64{"hedge_rate": {0, 2}, "stretch_verb_rate": {0, 2}}})
	got := map[string]string{}
	for _, f := range r.Findings {
		got[f.Metric] = jsonOf(f.Evidence)
	}
	if !contains(got["hedge_rate"], "grundsätzlich") || !contains(got["hedge_rate"], "letztlich") {
		t.Errorf("hedge evidence: %s", got["hedge_rate"])
	}
	if !contains(got["stretch_verb_rate"], "zur anwendung") {
		t.Errorf("stretch-verb evidence: %s", got["stretch_verb_rate"])
	}
	if text := FindingsText(r); !contains(text, "'grundsätzlich'") {
		t.Errorf("the revision order names the phrases: %s", text)
	}
	en := Measure("There is a cat. There is a dog. It is possible to see both.", "en")
	if en.Evidence["stretch_verb_rate"]["there is"] != 2 {
		t.Errorf("evidence counts: %v", en.Evidence)
	}
}

func TestProfileBlock(t *testing.T) {
	md := "# Style\n\n## Voice\n\n1. Short.\n\n```style-profile\n{\"schema\":\"covey-style/1\",\"language\":\"de\",\"documents\":3,\"words\":100,\"bands\":{\"sent_len_mean\":[10,18]}}\n```\n"
	p, prose, err := ParseProfile(md)
	if err != nil {
		t.Fatal(err)
	}
	if p.Language != "de" || p.Bands["sent_len_mean"] != [2]float64{10, 18} || prose != "# Style\n\n## Voice\n\n1. Short." {
		t.Errorf("parsed %+v / %q", p, prose)
	}
	if got := StripProfileBlock(md); got != "# Style\n\n## Voice\n\n1. Short." {
		t.Errorf("strip: %q", got)
	}
	if _, _, err := ParseProfile("# nothing here"); err != ErrNoProfile {
		t.Errorf("want ErrNoProfile, got %v", err)
	}
}

func TestProfilesByLanguage(t *testing.T) {
	de := "```style-profile\n{\"schema\":\"covey-style/1\",\"language\":\"de\",\"bands\":{\"commas_per_sentence\":[1.0,1.5]}}\n```"
	en := "```style-profile\n{\"schema\":\"covey-style/1\",\"language\":\"en\",\"bands\":{\"commas_per_sentence\":[0.4,0.9]}}\n```"
	ps := ParseProfiles("# Soul\n\n## Tone\n\n" + de + "\n\n" + en + "\n")
	if len(ps) != 2 || ps[0].Language != "de" || ps[1].Language != "en" {
		t.Fatalf("ParseProfiles: %+v", ps)
	}
	if p, ok := PickProfile(ps, DetectLanguage("The cat sat on the mat and it was fine, and the dog was not there.")); !ok || p.Language != "en" {
		t.Errorf("English text should pick the English profile, got %+v %v", p, ok)
	}
	if p, ok := PickProfile(ps, DetectLanguage("Der Hund lag auf der Matte und die Katze war nicht da, und das war gut so.")); !ok || p.Language != "de" {
		t.Errorf("German text should pick the German profile, got %+v %v", p, ok)
	}
	if _, ok := PickProfile(ps[:1], "en"); ok {
		t.Error("no English profile: nothing to pick")
	}
	if p, ok := PickProfile([]Profile{{Language: ""}}, "en"); !ok || p.Language != "" {
		t.Error("a profile without a language counts for every text")
	}
}

func TestSegmentationEdges(t *testing.T) {
	s := splitSentences("Das kostet z. B. 3.5 Euro am 3. Mai. Dr. Müller kam bzw. ging. Ende.")
	if len(s) != 3 {
		t.Errorf("sentences: %q", s)
	}
	prose, headings, blocks := stripMarkdown("---\ntitle: x\n---\n# Kopf: eins\n\nText mit `code` und [link](http://x).\n\n```\ncode\n```\n", false)
	if len(headings) != 1 || headings[0] != "Kopf: eins" || blocks != 1 || !contains(prose, "CODE") || !contains(prose, "link") || contains(prose, "http://x") {
		t.Errorf("strip: %q %v %d", prose, headings, blocks)
	}
	prose, _, _ = stripMarkdown("Lernprozesses**.** Formatives Feedback. Bewertung werden**.** Ende.", false)
	if contains(prose, "*") {
		t.Errorf("two bold periods on one line must strip separately: %q", prose)
	}
	prose, _, _ = stripMarkdown("Intro text here.\n\n- erstens ein Punkt mit Text\n- zweitens ein Punkt mit Text\n- drittens ein Punkt mit Text\n", false)
	if n := len(splitParagraphs(prose)); n != 4 {
		t.Errorf("list items as paragraphs: %d", n)
	}
	kinds := map[string]bool{}
	for _, a := range findAnchors("Thomas Kern zahlt 300 Euro, zum Beispiel an GitLab, siehe https://x.y.", "de") {
		kinds[a.Kind] = true
	}
	for _, k := range []string{"name", "number", "example", "url"} {
		if !kinds[k] {
			t.Errorf("anchor kind %s missing: %v", k, kinds)
		}
	}
	var numbers, names []string
	for _, a := range findAnchors("Bis 2023 stand alles auf Papier. Es dauerte 41 h und kostete 5 %. Export als PDF gemäß DSGVO, siehe CODE.\nThomas Kern kam.", "de") {
		switch a.Kind {
		case "number":
			numbers = append(numbers, a.Text)
		case "name":
			names = append(names, a.Text)
		}
	}
	if jsonOf(numbers) != `["2023","41 h","5 %"]` {
		t.Errorf("numbers: %v", numbers)
	}
	if jsonOf(names) != `["PDF","DSGVO","Thomas Kern"]` {
		t.Errorf("names: %v", names)
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
