package notify

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// compose needs no database: it turns the rows into the mail. What it has to
// get right is that text and HTML say the same thing, that the links are
// built from the address it was handed, and that a title somebody else wrote
// cannot become markup (#180).
func TestComposeCarriesLinksAndEscapes(t *testing.T) {
	s := &Sender{}
	id := uuid.New()
	items := []item{
		{id: id, kind: KindApproval, title: "Melder <b>waits</b>: http.post", link: "/inbox"},
		{id: id, kind: KindBudget, title: "no link"},
	}
	msg := s.compose("en", "Northgate", "Erika", ClassDecision, items, "https://covey.example.test")

	if !strings.Contains(msg.Body, "https://covey.example.test/inbox") {
		t.Errorf("the text has no link:\n%s", msg.Body)
	}
	if !strings.Contains(msg.Body, "https://covey.example.test/profile") {
		t.Errorf("the text does not point at the settings:\n%s", msg.Body)
	}
	for _, want := range []string{
		`<a href="https://covey.example.test/inbox"`,
		"Melder &lt;b&gt;waits&lt;/b&gt;: http.post",
		`<a href="https://covey.example.test/profile"`,
		"<title>Waiting for your decision</title>",
	} {
		if !strings.Contains(msg.HTML, want) {
			t.Errorf("the HTML lacks %q:\n%s", want, msg.HTML)
		}
	}
	if strings.Contains(msg.HTML, "<b>waits</b>") {
		t.Error("a title became markup")
	}

	// Without an address there is no link — and no half-link either.
	bare := s.compose("de", "covey", "Erika", ClassDecision, items, "")
	if strings.Contains(bare.Body, "/inbox") || strings.Contains(bare.HTML, "href=") {
		t.Errorf("links without a host:\n%s\n%s", bare.Body, bare.HTML)
	}
}
