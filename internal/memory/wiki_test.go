package memory

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncationRuneSafe: lange Umlaut-Texte dürfen nicht mitten in einem Rune
// geschnitten werden (sonst landet ungültiges UTF-8 im Prompt/Titel).
func TestTruncationRuneSafe(t *testing.T) {
	long := strings.Repeat("ä", 900) // 900 Runes = 1800 Bytes
	if got := FormatForPrompt([]Entry{{Title: "T", Slug: "s", Content: long}}); !utf8.ValidString(got) {
		t.Fatalf("FormatForPrompt liefert ungültiges UTF-8")
	}
	if got := deriveTitle(long); !utf8.ValidString(got) || utf8.RuneCountInString(got) > 80 {
		t.Fatalf("deriveTitle: ungültiges UTF-8 oder zu lang: %d Runes", utf8.RuneCountInString(got))
	}
}

func TestExtractLinks(t *testing.T) {
	body := "Kunde ACME, betreut von [[Kollegin Zabel]]. Siehe auch [[projekt-x]] und " +
		"[[Kollegin Zabel]] (Dublette) sowie [[Ticket 42|das Login-Ticket]]."
	got := extractLinks(body)
	want := []string{"kollegin-zabel", "projekt-x", "ticket-42"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractLinks = %v, want %v", got, want)
	}
	if got := extractLinks("keine links hier"); len(got) != 0 {
		t.Fatalf("ohne Links → leer, got %v", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Kunde ACME":            "kunde-acme",
		"Über/Löschung & Ärger": "ueber-loeschung-aerger",
		"   ":                   "seite",
		"Ticket #42!":           "ticket-42",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatIndexForPrompt(t *testing.T) {
	if got := FormatIndexForPrompt(nil); got != "" {
		t.Fatalf("leerer Index → leerer String, got %q", got)
	}
	got := FormatIndexForPrompt([]Entry{
		{Slug: "kunde-acme", Title: "Kunde ACME"},
		{Slug: "projekt-x", Title: "Projekt X"},
	})
	for _, want := range []string{"Dein Wiki (Index)", "- [[kunde-acme]] — Kunde ACME", "- [[projekt-x]] — Projekt X"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Index enthält %q nicht: %q", want, got)
		}
	}
}

func TestUnionSlugs(t *testing.T) {
	got := unionSlugs([]string{"a", "b"}, []string{"b", "c"}, nil, []string{"", "c", "d"})
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unionSlugs = %v, want %v (dedup, Reihenfolge, kein Leerstring)", got, want)
	}
	if got := unionSlugs(); len(got) != 0 {
		t.Fatalf("ohne Eingabe → leer (nicht nil-Panic), got %v", got)
	}
}

func TestDeriveTitle(t *testing.T) {
	if got := deriveTitle("Kunde ACME reagiert nur telefonisch. Rest egal."); got != "Kunde ACME reagiert nur telefonisch" {
		t.Fatalf("deriveTitle Satzgrenze: %q", got)
	}
	long := "Dies ist ein sehr langer Satz ohne Satzzeichen der weit über achtzig Zeichen hinausgeht und gekürzt werden muss"
	if got := deriveTitle(long); utf8.RuneCountInString(got) > 80 {
		t.Fatalf("deriveTitle muss auf 80 Zeichen kürzen, got %d: %q", utf8.RuneCountInString(got), got)
	}
}
