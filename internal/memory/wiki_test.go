package memory

import (
	"reflect"
	"testing"
)

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

func TestDeriveTitle(t *testing.T) {
	if got := deriveTitle("Kunde ACME reagiert nur telefonisch. Rest egal."); got != "Kunde ACME reagiert nur telefonisch" {
		t.Fatalf("deriveTitle Satzgrenze: %q", got)
	}
	long := "Dies ist ein sehr langer Satz ohne Satzzeichen der weit über achtzig Zeichen hinausgeht und gekürzt werden muss"
	if got := deriveTitle(long); len(got) > 80 {
		t.Fatalf("deriveTitle muss auf 80 Zeichen kürzen, got %d: %q", len(got), got)
	}
}
