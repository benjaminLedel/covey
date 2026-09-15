package voice

import (
	"strings"
	"testing"

	"covey/internal/style"
)

// A short corpus with the shapes a build has to tell apart: an opening, a
// paragraph carrying figures, one naming an example, a long one, a short one,
// and one that closes on an antithesis — the last of which must never become an
// exemplar.
func corpus() []Document {
	return []Document{
		{Name: "post-1.md", Text: `Am Montag stand die Auslieferung still, und niemand hatte eine Meldung
bekommen. Der Build lief durch, der Deploy meldete Erfolg, und trotzdem
antwortete die Instanz mit der Version von Freitag. Wir haben zwei Stunden
gebraucht, um das zu verstehen.

Die Zahlen dazu sind unspektakulär und trotzdem der Kern: 206.000 von 262.000
Ereignissen und 376 von 384 MB waren der wörtliche Verlauf der Läufe. Der Rest,
also alles, was jemand später wirklich liest, macht acht Megabyte aus. Wer die
Aufbewahrung diskutiert, diskutiert also über den Verlauf und über sonst nichts.

Ein Beispiel dafür ist der Agent, der seit sechs Wochen ohne Neustart lief. Er
arbeitete mit dem Code von vorgestern, weil seine Sandbox das Image behielt, mit
dem sie gestartet war. Nichts an der Oberfläche sagte das, und die Fußzeile der
Steuerebene zeigte brav den neuen Commit.

Das Ergebnis war schnell da. Es war nur falsch.`},
		{Name: "post-2.md", Text: `Die zweite Woche fing an wie die erste, mit einer Meldung um 6:40 Uhr und
einem Ticket, das niemand zuordnen konnte. Diesmal lag es an der Uhr: Der Runner
stand auf UTC, die Steuerebene auf Europe/Berlin, und die Differenz von zwei
Stunden fiel erst auf, als ein Heartbeat doppelt lief.

Wir haben danach drei Dinge geändert, und nur eines davon war Code. Die
Zeitzone steht jetzt in der Konfiguration des Runners, der Heartbeat protokolliert
seine Basiszeit, und die Dokumentation nennt beide Stellen nebeneinander. Das
dritte hat am meisten gebracht, obwohl es nichts repariert.

Zum Beispiel merkt man den Unterschied beim nächsten Vorfall: Wer die Basiszeit
im Protokoll sieht, sucht nicht mehr eine Stunde lang im falschen Prozess. Das
ist der ganze Trick an solchen Zeilen, und er kostet drei Minuten beim
Schreiben.

Seitdem ist es ruhig. Zwei Vorfälle in acht Wochen, beide in unter zwanzig
Minuten erklärt, keiner davon mit einer Überraschung im Kern.`},
		{Name: "post-3.md", Text: `Eine Frage, die mir seit Jahren gestellt wird: Wie viele Agenten braucht ein
Team? Die ehrliche Antwort ist, dass die Zahl nicht das Problem ist. Das Problem
ist, wer den Staffelstab hält, wenn zwei davon dieselbe Aufgabe anfassen.

Im September haben wir es gemessen. Von 74 Aufgaben liefen 61 durch eine
einzige Hand, 11 über zwei und 2 über drei. Die beiden Dreierketten haben
zusammen so lange gebraucht wie die 61 anderen, und in beiden Fällen war die
Übergabe der Grund, nicht die Arbeit.

Die Regel, die daraus wurde, steht in zwei Sätzen im Handbuch. Sie ist keine
Erkenntnis, sie ist eine Buchhaltung.`},
		{Name: "post-4.md", Text: `Der Mittwoch gehörte dem Speicher. Ein Home von 7,3 GB wurde bei jedem
Wecken gescannt, und der Scan dauerte elf Minuten, bevor überhaupt etwas
passierte. Das ist keine Schätzung, das ist die gemessene Zeit auf der Maschine
in Frankfurt.

Die Lösung war unspektakulär: Blöcke statt Dateien, und ein Marker, der sagt,
ob die Kopie jünger ist als der Snapshot. Danach lag dieselbe Operation bei
achtzehn Sekunden. Der Agent merkte davon nichts, was der Punkt ist.

Ein kurzer Nachtrag, weil die Frage kam: Nein, der Snapshot ersetzt kein
Backup.`},
	}
}

func TestBuildMeasuresTheCorpusAndChoosesVariedExemplars(t *testing.T) {
	b, err := Build(corpus(), "de", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Profile.Language != "de" {
		t.Errorf("language = %q", b.Profile.Language)
	}
	if len(b.Profile.Bands) == 0 {
		t.Fatal("a voice without bands is not a guard")
	}
	if b.Words == 0 || b.Profile.Words != b.Words {
		t.Errorf("word count: built %d, profile %d", b.Words, b.Profile.Words)
	}
	if len(b.Exemplars) < 3 {
		t.Fatalf("too few exemplars: %d", len(b.Exemplars))
	}
	// Variety is the point: a set that is six times the same role shows a model
	// one move, which is what bands already fail to prevent.
	roles := map[string]int{}
	texts := map[string]int{}
	for _, ex := range b.Exemplars {
		roles[ex.Role]++
		texts[ex.Text]++
		if texts[ex.Text] > 1 {
			t.Errorf("the same passage twice: %q", ex.Text[:40])
		}
		if style.ClosesOnAntithesis(ex.Text) {
			t.Errorf("a passage closing on an antithesis is never an exemplar: %q", ex.Text)
		}
	}
	if len(roles) < 3 {
		t.Errorf("the exemplars cover %d roles: %+v", len(roles), roles)
	}
	// Without a reference there is no contrast, and the build says so rather
	// than leaving an empty list to be read as "nothing to say".
	if len(b.Contrast) != 0 {
		t.Errorf("no reference, and yet a contrast: %+v", b.Contrast)
	}
	if !hasNote(b.Notes, "no reference corpus") {
		t.Errorf("the missing reference has to be reported: %+v", b.Notes)
	}
}

// A rebuild after a metric change has to be safe, and that rests on the build
// being deterministic apart from the card.
func TestBuildIsDeterministic(t *testing.T) {
	a, err := Build(corpus(), "de", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(corpus(), "de", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Exemplars) != len(b.Exemplars) {
		t.Fatalf("different number of exemplars: %d vs %d", len(a.Exemplars), len(b.Exemplars))
	}
	for i := range a.Exemplars {
		if a.Exemplars[i] != b.Exemplars[i] {
			t.Fatalf("exemplar %d differs between two builds of the same corpus", i)
		}
	}
	for metric, band := range a.Profile.Bands {
		if b.Profile.Bands[metric] != band {
			t.Fatalf("band %s differs between two builds", metric)
		}
	}
}

// Fewer than four usable documents is allowed and has to be said: the bands are
// then a padded min..max, and whoever reads a finding against them should know
// on how much they rest.
func TestASmallCorpusSaysWhatItsBandsAre(t *testing.T) {
	b, err := Build(corpus()[:2], "de", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasNote(b.Notes, "band") {
		t.Errorf("a small corpus has to report its own bands: %+v", b.Notes)
	}
}

// The contrast is the sharpest half of a voice, and it falls out of a
// comparison rather than being written by anybody.
func TestContrastNamesWhereTheReferenceSitsOutsideTheBand(t *testing.T) {
	b, err := Build(corpus(), "de", nil)
	if err != nil {
		t.Fatal(err)
	}
	metric := "dash_per_1000"
	band, ok := b.Profile.Bands[metric]
	if !ok {
		t.Skip("this corpus has no band for " + metric)
	}
	// A reference far above the author's upper edge — the dash is the habit the
	// measurements found in every AI corpus and in almost no human one.
	reference := map[string]float64{metric: band[1] + 10*(band[1]-band[0]) + 10}
	withRef, err := Build(corpus(), "de", reference)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range withRef.Contrast {
		if c.Metric == metric {
			found = true
			if c.Other <= c.Author {
				t.Errorf("the contrast has to name both sides: %+v", c)
			}
		}
	}
	if !found {
		t.Fatalf("the metric the reference is far outside is not in the contrast: %+v", withRef.Contrast)
	}
}

// What an agent carries is the rendered file, and the gate has to be able to
// read its profile back out of it — otherwise the guard behind the voice is
// missing without anything saying so.
func TestRenderCarriesTheCardTheExemplarsAndAProfileTheGateCanRead(t *testing.T) {
	b, err := Build(corpus(), "de", nil)
	if err != nil {
		t.Fatal(err)
	}
	v := Voice{
		Name: "Hausstimme", Language: "de", Version: 1,
		Profile: b.Profile, Exemplars: b.Exemplars, Contrast: b.Contrast,
		Card:         "ein Entwurf, den niemand freigegeben hat",
		ReleasedCard: "Der Autor öffnet mit einer Beobachtung.",
		Words:        b.Words, Documents: b.Docs,
	}
	out := Render(v)
	if !strings.Contains(out, "Der Autor öffnet mit einer Beobachtung.") {
		t.Error("the released card belongs in the file")
	}
	// The draft must not: it is what a model wrote and nobody signed.
	if strings.Contains(out, "ein Entwurf, den niemand freigegeben hat") {
		t.Error("an unreleased card must not reach a prompt")
	}
	if !strings.Contains(out, b.Exemplars[0].Text[:30]) {
		t.Error("the exemplars belong in the file")
	}
	parsed, prose, err := style.ParseProfile(out)
	if err != nil {
		t.Fatalf("the gate cannot read the profile back: %v", err)
	}
	if len(parsed.Bands) != len(b.Profile.Bands) {
		t.Errorf("bands lost in rendering: %d vs %d", len(parsed.Bands), len(b.Profile.Bands))
	}
	if !strings.Contains(prose, "Hausstimme") {
		t.Error("the prose above the block is what the model reads — it has to name the voice")
	}
}

func TestAnEmptyCorpusIsRefusedWithASentence(t *testing.T) {
	if _, err := Build(nil, "de", nil); err == nil {
		t.Fatal("a voice without texts is not a voice")
	}
}

func hasNote(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

// How often a text says "Sie" or "wir" is a property of the register, not of
// the hand: the same author says "Sie" in a tender and "wir" in a blog post. A
// band built from one of the two would report the other as a finding on every
// paragraph, so those metrics are measured and not banded (spec/24).
func TestAddressMetricsAreMeasuredAndNotBanded(t *testing.T) {
	b, err := Build(corpus(), "de", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range addressMetrics {
		if _, banded := b.Profile.Bands[metric]; banded {
			t.Errorf("%s must not carry a band", metric)
		}
	}
	// Measured all the same: the figure is orientation for whoever reads the
	// profile, it just does not decide anything.
	if _, ok := b.Profile.Corpus["wir_per_1000"]; !ok {
		t.Error("the address figures still belong in the corpus values")
	}
}

// What counts as a correction, and what is only a comma. The threshold is not
// taste: a pair that differs by one word teaches nothing and costs room in
// every card prompt of every build.
func TestWhatCountsAsACorrection(t *testing.T) {
	lang := "Die Migration verzögert sich um zwei Wochen, weil die Datenbank auf dem alten " +
		"Server liegt und erst umgezogen werden muss. Wir nennen den neuen Termin am Freitag."
	tests := []struct {
		name       string
		before     string
		after      string
		want       bool
		wantReason string
	}{
		{"a real rewrite", lang,
			"Die Migration dauert zwei Wochen länger. Der Grund liegt in der Datenbank auf dem " +
				"alten Server, die zuerst umziehen muss. Am Freitag steht der neue Termin.", true, ""},
		{"the same text", lang, lang, false, "same text"},
		{"one half missing", lang, "", false, "both halves"},
		{"a typo in a long text", lang,
			strings.Replace(lang, "Datenbank", "Datenbanken", 1), false, "typo"},
		{"too short to have a style", "Kurz und knapp.", "Knapp und kurz.", false, "no style"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := IsCorrection(tc.before, tc.after)
			if got != tc.want {
				t.Fatalf("IsCorrection = %v (%s), expected %v", got, reason, tc.want)
			}
			if !got && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("the refusal has to say what to do differently: %q", reason)
			}
		})
	}
}

// A reordered sentence is not a rewrite. The count is over words, not over
// positions, because what a voice learns from is different WORDING.
func TestMovingASentenceIsNotAChange(t *testing.T) {
	a := "Der Scan dauerte elf Minuten. Die Lösung waren Blöcke statt Dateien. " +
		"Danach lag dieselbe Operation bei achtzehn Sekunden, und der Agent merkte nichts davon."
	b := "Die Lösung waren Blöcke statt Dateien. Der Scan dauerte elf Minuten. " +
		"Danach lag dieselbe Operation bei achtzehn Sekunden, und der Agent merkte nichts davon."
	if got, reason := IsCorrection(a, b); got {
		t.Errorf("a reordered text is not a correction (%q)", reason)
	}
}
