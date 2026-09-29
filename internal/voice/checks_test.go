package voice

import (
	"strings"
	"testing"
)

// blog is a German paragraph of the blog register: first person plural,
// nobody addressed, sentences of fifteen to twenty words.
const blog = `Am Montag stand die Auslieferung still, und niemand im Team hatte vorher eine Meldung bekommen. ` +
	`Der Build lief durch, der Deploy meldete Erfolg, und trotzdem antwortete die Instanz mit der alten Version. ` +
	`Wir haben zwei Stunden gebraucht, um zu verstehen, dass die Sandbox ihr Image seit Wochen behalten hatte. `

// mail is a German paragraph that addresses its reader with "Sie".
const mail = `Vielen Dank für Ihre Nachricht vom Montag, die wir gern an die zuständige Stelle weitergegeben haben. ` +
	`Wir bitten Sie, uns die Rechnungsnummer zu nennen, damit wir Ihnen die Gutschrift rasch zuordnen können. ` +
	`Sollten Sie Fragen haben, erreichen Sie uns werktags zwischen acht und siebzehn Uhr unter der bekannten Nummer. `

func repeat(s string, n int) string { return strings.Repeat(s, n) }

func codes(cs []Check) map[string]Check {
	out := map[string]Check{}
	for _, c := range cs {
		out[c.Code] = c
	}
	return out
}

func TestCheckCorpusWithoutTextsBlocks(t *testing.T) {
	cs := CheckCorpus(nil, "de")
	if len(cs) != 1 || cs[0].Code != CheckNoTexts || cs[0].Level != LevelBlock {
		t.Fatalf("%+v", cs)
	}
}

// Two long texts and one short: two more are needed for percentile bands,
// and the short one is named as not carrying a band.
func TestCheckCorpusSaysWhatIsMissing(t *testing.T) {
	docs := []Document{
		{Name: "a.md", Text: repeat(blog, 4)},
		{Name: "b.md", Text: repeat(blog, 4)},
		{Name: "kurz.md", Text: blog},
	}
	c := codes(CheckCorpus(docs, "de"))
	if m, ok := c[CheckMoreTexts]; !ok || m.N != 2 {
		t.Errorf("more texts: %+v", c)
	}
	if s, ok := c[CheckShortTexts]; !ok || len(s.Docs) != 1 || s.Docs[0] != "kurz.md" {
		t.Errorf("short texts: %+v", c)
	}
	if _, ok := c[CheckRegister]; ok {
		t.Errorf("one register only: %+v", c[CheckRegister])
	}
}

// A mail that says "Sie" among blog posts that address nobody is neutral — a
// text that addresses nobody fits both. One that says "du" among ones that
// say "Sie" is a different register, and the minority is named.
func TestCheckCorpusNamesADifferentRegister(t *testing.T) {
	du := strings.NewReplacer("Ihre", "deine", "Ihnen", "dir", "Sie", "du").Replace(mail)
	docs := []Document{
		{Name: "mail-1.md", Text: repeat(mail, 4)},
		{Name: "mail-2.md", Text: repeat(mail, 4)},
		{Name: "mail-3.md", Text: repeat(mail, 4)},
		{Name: "chat.md", Text: repeat(du, 4)},
	}
	c := codes(CheckCorpus(docs, "de"))
	r, ok := c[CheckRegister]
	if !ok || len(r.Docs) != 1 || r.Docs[0] != "chat.md" || r.Detail != "informal" {
		t.Fatalf("register: %+v", c)
	}
	neutral := []Document{
		{Name: "blog-1.md", Text: repeat(blog, 4)},
		{Name: "blog-2.md", Text: repeat(blog, 4)},
		{Name: "mail.md", Text: repeat(mail, 4)},
	}
	if _, ok := codes(CheckCorpus(neutral, "de"))[CheckRegister]; ok {
		t.Error("a text that addresses nobody is not a register of its own")
	}
}

func TestCheckCorpusNamesOddSentences(t *testing.T) {
	legal := "Die Haftung des Anbieters für leicht fahrlässige Pflichtverletzungen ist, soweit nicht Leben, " +
		"Körper oder Gesundheit betroffen sind und soweit keine wesentlichen Vertragspflichten verletzt werden, " +
		"deren Erfüllung die ordnungsgemäße Durchführung des Vertrages überhaupt erst ermöglicht und auf deren " +
		"Einhaltung der Vertragspartner regelmäßig vertrauen darf, in jedem Fall der Höhe nach begrenzt. "
	docs := []Document{
		{Name: "a.md", Text: repeat(blog, 4)},
		{Name: "b.md", Text: repeat(blog, 4)},
		{Name: "c.md", Text: repeat(blog, 4)},
		{Name: "agb.md", Text: repeat(legal, 4)},
	}
	s, ok := codes(CheckCorpus(docs, "de"))[CheckSentenceLength]
	if !ok || len(s.Docs) != 1 || s.Docs[0] != "agb.md" {
		t.Fatalf("sentence length: %+v", CheckCorpus(docs, "de"))
	}
}

func TestCheckCorpusEnough(t *testing.T) {
	var docs []Document
	for _, n := range []string{"a", "b", "c", "d"} {
		docs = append(docs, Document{Name: n, Text: repeat(blog, 4)})
	}
	cs := CheckCorpus(docs, "de")
	if len(cs) != 1 || cs[0].Code != CheckEnough || cs[0].Level != LevelOK {
		t.Fatalf("%+v", cs)
	}
}

func TestCheckCorpusNamesAnotherLanguage(t *testing.T) {
	en := "On Monday the delivery stood still, and nobody on the team had received a warning before it happened. " +
		"The build passed, the deploy reported success, and still the instance answered with the old version. "
	docs := []Document{
		{Name: "a.md", Text: repeat(blog, 4)},
		{Name: "b.md", Text: repeat(blog, 4)},
		{Name: "en.md", Text: repeat(en, 5)},
	}
	l, ok := codes(CheckCorpus(docs, "de"))[CheckLanguage]
	if !ok || len(l.Docs) != 1 || l.Docs[0] != "en.md" {
		t.Fatalf("language: %+v", CheckCorpus(docs, "de"))
	}
}
