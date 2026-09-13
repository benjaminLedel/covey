package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"covey/internal/style"
)

// `covey style` is the measurement on the command line, without a database —
// which is what makes it usable where a text is actually written.
func TestStyleUsageAndUnknownSubcommand(t *testing.T) {
	if err := runStyle(nil); err == nil {
		t.Error("without arguments the usage has to appear")
	}
	err := runStyle([]string{"messen"})
	if err == nil || !strings.Contains(err.Error(), "messen") {
		t.Errorf("an unknown subcommand was accepted: %v", err)
	}
}

func TestStyleStatsFlagErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--holdout"},
		{"--lang"},
	} {
		if err := runStyleStats(args); err == nil {
			t.Errorf("%v was accepted although the value is missing", args)
		}
	}
	// A path that yields no document is an error and not an empty table: an
	// empty table looks like a measurement that found nothing to complain about.
	if err := runStyleStats([]string{t.TempDir()}); err == nil {
		t.Error("a directory without documents produced a result")
	}
	if err := runStyleStats([]string{filepath.Join(t.TempDir(), "gibtesnicht.md")}); err == nil {
		t.Error("a path that is not there produced a result")
	}
}

func TestStyleCheckArgumentErrors(t *testing.T) {
	if err := runStyleCheck(nil); err == nil {
		t.Error("check without a file was accepted")
	}
	if err := runStyleCheck([]string{"--profile"}); err == nil {
		t.Error("--profile without a value was accepted")
	}
	if err := runStyleCheck([]string{filepath.Join(t.TempDir(), "gibtesnicht.md")}); err == nil {
		t.Error("a file that is not there was measured")
	}
	// A profile that is not one has to name the file — otherwise the reader
	// looks for the fault in the text.
	dir := t.TempDir()
	text := filepath.Join(dir, "text.md")
	os.WriteFile(text, []byte("Ein Satz."), 0o600)
	profile := filepath.Join(dir, "TONE.md")
	os.WriteFile(profile, []byte("kein Profil"), 0o600)
	err := runStyleCheck([]string{text, "--profile", profile})
	if err == nil || !strings.Contains(err.Error(), "TONE.md") {
		t.Errorf("the error does not name the profile: %v", err)
	}
}

// collectDocuments is what decides which files are measured at all. Its two
// rules are worth keeping: README.md is not a sample of anybody's style, and a
// holdout is named so it can be left out of its own corpus.
func TestCollectDocuments(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"eins.md":       "Der erste Text.",
		"zwei.txt":      "Der zweite Text.",
		"README.md":     "Die Anleitung.",
		"notiz.json":    `{"kein":"dokument"}`,
		"drei.markdown": "Der dritte Text.",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	docs, err := collectDocuments([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range docs {
		names[filepath.Base(d.path)] = true
	}
	for _, want := range []string{"eins.md", "zwei.txt", "drei.markdown"} {
		if !names[want] {
			t.Errorf("%s was not collected", want)
		}
	}
	// The project's own README is not a sample of the style being measured.
	if names["README.md"] {
		t.Error("README.md was measured along")
	}
	if names["notiz.json"] {
		t.Error("a file that is not a document was collected")
	}

	// The holdout is named by base name so that a profile can be built without
	// the text it will later be checked against.
	held, err := collectDocuments([]string{dir}, []string{"eins.md"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range held {
		if filepath.Base(d.path) == "eins.md" {
			t.Error("the holdout was measured along")
		}
	}

	// A single file is taken as it is, README or not — whoever names it means it.
	single, err := collectDocuments([]string{filepath.Join(dir, "README.md")}, nil)
	if err != nil || len(single) != 1 {
		t.Fatalf("a named file was not taken: %v, %v", single, err)
	}

	if _, err := collectDocuments([]string{filepath.Join(dir, "gibtesnicht.md")}, nil); err == nil {
		t.Error("a path that is not there was accepted")
	}
}

func TestReadDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "text.md")
	os.WriteFile(path, []byte("Inhalt"), 0o600)
	got, err := readDocument(path)
	if err != nil || got != "Inhalt" {
		t.Fatalf("readDocument = %q, %v", got, err)
	}
	// An office document without pandoc says what is missing rather than
	// producing an empty measurement.
	if _, err := exec_lookPandoc(); err != nil {
		docx := filepath.Join(dir, "brief.docx")
		os.WriteFile(docx, []byte("PK"), 0o600)
		_, err := readDocument(docx)
		if err == nil || !strings.Contains(err.Error(), "pandoc") {
			t.Errorf("a missing pandoc was not named: %v", err)
		}
	}
}

func TestMajorityLanguage(t *testing.T) {
	de := []style.Measurement{{Language: "de"}, {Language: "de"}, {Language: "en"}}
	if got := majorityLanguage(de); got != "de" {
		t.Errorf("majorityLanguage = %q", got)
	}
	en := []style.Measurement{{Language: "en"}, {Language: "en"}, {Language: "de"}}
	if got := majorityLanguage(en); got != "en" {
		t.Errorf("majorityLanguage = %q", got)
	}
	// A tie falls to German, and so does an empty corpus: the default is a
	// decision, not an accident.
	if got := majorityLanguage([]style.Measurement{{Language: "de"}, {Language: "en"}}); got != "de" {
		t.Errorf("a tie gave %q", got)
	}
	if got := majorityLanguage(nil); got != "de" {
		t.Errorf("an empty corpus gave %q", got)
	}
}

// pickBands keeps exactly the metrics a profile has bands for — a corpus value
// without a band would be a number in the file that nothing reads.
func TestPickBands(t *testing.T) {
	corpus := map[string]float64{"erfunden_metrik": 1}
	var known string
	for k := range style.Label {
		known = k
		break
	}
	if known == "" {
		t.Skip("the style package declares no labelled metric")
	}
	corpus[known] = 2
	got := pickBands(corpus)
	if _, ok := got["erfunden_metrik"]; ok {
		t.Error("a metric without a band was kept")
	}
	if got[known] != 2 {
		t.Errorf("the labelled metric is missing: %v", got)
	}
}

func TestTrimNum(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0, "0"}, {1, "1"}, {1.5, "1.5"}, {1.250, "1.25"}, {0.001, "0.001"},
		// Below the third decimal everything is zero, and "0" is the honest
		// rendering of that — not "0.000".
		{0.0001, "0"},
		{-2.5, "-2.5"},
	} {
		if got := trimNum(tc.in); got != tc.want {
			t.Errorf("trimNum(%v) = %q, expected %q", tc.in, got, tc.want)
		}
	}
}

// The table is what somebody reads at the terminal. It has to carry every
// metric it promises, and the corpus column only when there is more than one
// document to aggregate.
func TestPrintStatsTable(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "eins.md")
	os.WriteFile(one, []byte("Ein Satz. Noch ein Satz."), 0o600)
	docs, err := collectDocuments([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ms []style.Measurement
	for _, d := range docs {
		ms = append(ms, style.Measure(d.text, "de"))
	}
	out := captureStdout(t, func() { printStatsTable(docs, ms, style.Aggregate(ms)) })
	for _, want := range append([]string{"words", "eins.md"}, statsKeys...) {
		if !strings.Contains(out, want) {
			t.Errorf("the table is missing %q", want)
		}
	}
	if strings.Contains(out, "CORPUS") {
		t.Error("a single document got a corpus column of its own")
	}

	two := filepath.Join(dir, "zwei.md")
	os.WriteFile(two, []byte("Ein weiterer Satz. Und noch einer."), 0o600)
	docs, _ = collectDocuments([]string{dir}, nil)
	ms = nil
	for _, d := range docs {
		ms = append(ms, style.Measure(d.text, "de"))
	}
	out = captureStdout(t, func() { printStatsTable(docs, ms, style.Aggregate(ms)) })
	if !strings.Contains(out, "CORPUS") {
		t.Error("two documents got no corpus column")
	}
	_ = one
}

// A long file name is cut so the columns stay aligned — a table that slips is
// a table nobody reads.
func TestPrintStatsTableShortensLongNames(t *testing.T) {
	dir := t.TempDir()
	long := filepath.Join(dir, "ein-sehr-langer-dateiname-der-nicht-passt.md")
	os.WriteFile(long, []byte("Ein Satz."), 0o600)
	docs, err := collectDocuments([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ms := []style.Measurement{style.Measure(docs[0].text, "de")}
	out := captureStdout(t, func() { printStatsTable(docs, ms, style.Aggregate(ms)) })
	if strings.Contains(out, "ein-sehr-langer-dateiname-der-nicht-passt.md") {
		t.Error("the long name was printed in full")
	}
	if !strings.Contains(out, "ein-sehr-langer-da") {
		t.Errorf("the name was not shortened to 18 characters:\n%s", out)
	}
}

// `covey style stats --profile` is the documented way to produce the
// ```style-profile``` block for a TONE.md (spec/06). What it writes is read
// back by the style gate, so the block has to be a block the parser accepts —
// a profile that only looks right is a gate that never applies.
func TestStyleStatsWritesAProfileTheParserAccepts(t *testing.T) {
	dir := t.TempDir()
	for name, text := range corpus() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() {
		if err := runStyleStats([]string{dir, "--profile", "--lang", "de"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "```style-profile") {
		t.Fatalf("no profile block was written:\n%s", out)
	}
	p, _, err := style.ParseProfile(out)
	if err != nil {
		t.Fatalf("the written profile does not parse: %v\n%s", err, out)
	}
	if p.Language != "de" {
		t.Errorf("the profile names the language %q", p.Language)
	}
	if len(p.Bands) == 0 {
		t.Error("the profile carries no bands — a gate with none applies to nothing")
	}
	if p.Documents == 0 {
		t.Error("the profile says it was built from no documents")
	}
}

// A document named as the holdout is left out of the profile it will later be
// measured against — otherwise the corpus contains its own test.
func TestStyleStatsHonoursTheHoldout(t *testing.T) {
	dir := t.TempDir()
	for name, text := range corpus() {
		os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600)
	}
	out := captureStdout(t, func() {
		if err := runStyleStats([]string{dir, "--profile", "--holdout", "eins.md"}); err != nil {
			t.Fatal(err)
		}
	})
	p, _, err := style.ParseProfile(out)
	if err != nil {
		t.Fatal(err)
	}
	var held bool
	for _, h := range p.Holdout {
		if h == "eins.md" {
			held = true
		}
	}
	if !held {
		t.Errorf("the profile does not record the holdout: %v", p.Holdout)
	}
}

// --json is the machine-readable form of the same measurement: per document
// and the corpus, so a script can watch a number over time.
func TestStyleStatsAsJSON(t *testing.T) {
	dir := t.TempDir()
	for name, text := range corpus() {
		os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600)
	}
	out := captureStdout(t, func() {
		if err := runStyleStats([]string{dir, "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var doc struct {
		Documents []style.Measurement `json:"documents"`
		Corpus    map[string]float64  `json:"corpus"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json is not readable JSON: %v\n%s", err, out)
	}
	if len(doc.Documents) == 0 || len(doc.Corpus) == 0 {
		t.Errorf("--json carries %d documents and %d corpus values", len(doc.Documents), len(doc.Corpus))
	}
}

// corpus is four texts long enough to be measured (the profile wants 150 words
// per document before it counts one).
func corpus() map[string]string {
	para := func(seed string) string {
		var b strings.Builder
		for i := 0; i < 12; i++ {
			b.WriteString(seed + " Der Kunde Meier meldete am Montag einen Ausfall der Schnittstelle, und die Ursache war ein Zertifikat, das am Sonntag abgelaufen war. ")
			b.WriteString("Frau Zabel hat es am Dienstag erneuert, und seither läuft die Übertragung wieder. ")
		}
		return b.String()
	}
	return map[string]string{
		"eins.md": "# Eins\n\n" + para("Erstens."),
		"zwei.md": "# Zwei\n\n" + para("Zweitens."),
		"drei.md": "# Drei\n\n" + para("Drittens."),
		"vier.md": "# Vier\n\n" + para("Viertens."),
	}
}
