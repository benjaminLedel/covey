package mail

import (
	"strings"
	"testing"
)

func TestHeadingTakesTheSiteNameOff(t *testing.T) {
	for _, tc := range []struct{ subject, site, want string }{
		{"covey: adresse bestätigen", "covey", "Adresse bestätigen"},
		{"covey: Adresse bestätigen", "covey", "Adresse bestätigen"},
		// No prefix to take off: the subject stands as it is, capitalised.
		{"etwas anderes", "covey", "Etwas anderes"},
		// The prefix has to match exactly — a different site's name stays put.
		{"andere: Sache", "covey", "Andere: Sache"},
		{"", "covey", ""},
		// A non-ASCII first rune is upper-cased as a rune, not as a byte.
		{"covey: über nacht", "covey", "Über nacht"},
	} {
		if got := Heading(tc.subject, tc.site); got != tc.want {
			t.Errorf("Heading(%q, %q) = %q, expected %q", tc.subject, tc.site, got, tc.want)
		}
	}
}

func TestListLinksWholeLinesAndEscapesText(t *testing.T) {
	got := string(List([]Item{
		{Text: "Aufgabe <fertig>"},
		{Text: "Entscheidung", Href: "https://example.test/a?b=1&c=2"},
	}))
	if strings.Contains(got, "<fertig>") {
		t.Errorf("item text was not escaped:\n%s", got)
	}
	if !strings.Contains(got, "Aufgabe &lt;fertig&gt;") {
		t.Errorf("escaped text is missing:\n%s", got)
	}
	if !strings.Contains(got, `href="https://example.test/a?b=1&amp;c=2"`) {
		t.Errorf("address was not escaped into the anchor:\n%s", got)
	}
	// The line IS the link — no stray "here" at its end.
	if !strings.Contains(got, ">Entscheidung</a>") {
		t.Errorf("the item's own text is not the anchor text:\n%s", got)
	}
	if n := strings.Count(got, "<li"); n != 2 {
		t.Errorf("got %d items, expected 2:\n%s", n, got)
	}
}

func TestListWithoutItemsIsAnEmptyList(t *testing.T) {
	got := string(List(nil))
	if strings.Contains(got, "<li") {
		t.Errorf("empty list carries items:\n%s", got)
	}
	if !strings.HasPrefix(got, "<ul") || !strings.HasSuffix(got, "</ul>") {
		t.Errorf("list is not closed:\n%s", got)
	}
}

func TestRenderProducesCompleteHTML(t *testing.T) {
	got := Render("de", Page{
		Site:   "covey",
		Title:  "Adresse bestätigen",
		Body:   Paragraph("Hallo"),
		Footer: Paragraph("Fuß"),
	})
	for _, want := range []string{`lang="de"`, "covey", "Adresse bestätigen", "Hallo", "Fuß", "</html>"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered page is missing %q:\n%s", want, got)
		}
	}
	// Nothing is fetched from outside — that is the whole design of this part.
	for _, forbidden := range []string{"<img", "@import", "<script", "https://fonts."} {
		if strings.Contains(got, forbidden) {
			t.Errorf("the layout loads remote content (%q)", forbidden)
		}
	}
}
