package mail

import (
	"strings"
	"testing"
	"time"
)

// The subject is the only place where text somebody else wrote reaches a
// header. A smuggled-in line break there would be a header of their choosing —
// a Bcc, for instance.
func TestSubjectCannotSmuggleAHeader(t *testing.T) {
	msg := string(build("covey <no-reply@example.test>", "no-reply@example.test", Message{
		To:      "someone@example.test",
		Subject: "reset\r\nBcc: attacker@example.test",
		Body:    "text",
	}, time.Now()))

	head, _, _ := strings.Cut(msg, "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("the subject produced a header of its own:\n%s", head)
		}
	}
	// And it is not silently dropped either: the text stays in the subject, on
	// one line. Dropping it would hide the attempt; keeping it visible is what
	// puts it in front of the reader.
	if !strings.Contains(head, "Subject: reset  Bcc: attacker@example.test") {
		t.Errorf("the subject was not folded into one line:\n%s", head)
	}
}

func TestMessageShape(t *testing.T) {
	at := time.Date(2026, 9, 2, 10, 30, 0, 0, time.UTC)
	msg := string(build(`"covey test" <no-reply@example.test>`, "no-reply@example.test", Message{
		To:      "erika@example.test",
		Subject: "Grüße",
		Body:    "Zwei Zeilen,\nund ein Umlaut: ä\n",
	}, at))

	for _, want := range []string{
		`From: "covey test" <no-reply@example.test>`,
		"To: erika@example.test",
		"Auto-Submitted: auto-generated",
		"Content-Transfer-Encoding: quoted-printable",
		"@example.test>", // Message-ID takes the domain from the bare address
		"=C3=A4",         // the umlaut, quoted-printable
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message lacks %q:\n%s", want, msg)
		}
	}
	// The raw umlaut must not appear — an unencoded 8-bit byte is what makes a
	// message fail at the one relay that never got the memo.
	if strings.Contains(msg, "ä") {
		t.Error("the body carries a raw umlaut instead of quoted-printable")
	}
}

func TestAddressValidation(t *testing.T) {
	if _, err := address("recipient", "not an address"); err == nil {
		t.Error("a broken address was accepted")
	}
	got, err := address("recipient", "  Erika <erika@example.test> ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "erika@example.test" {
		t.Errorf("got %q, expected the bare address", got)
	}
}

// With an HTML part the message is multipart/alternative, text first; without
// one it stays the plain message it always was. Both parts are
// quoted-printable, so the umlaut test above holds for the HTML too.
func TestMultipartShape(t *testing.T) {
	msg := string(build("covey <no-reply@example.test>", "no-reply@example.test", Message{
		To:      "erika@example.test",
		Subject: "hello",
		Body:    "Zwei Zeilen: ä\n",
		HTML:    "<p>Zwei Zeilen: ä</p>",
	}, time.Now()))

	head, body, _ := strings.Cut(msg, "\r\n\r\n")
	if !strings.Contains(head, `Content-Type: multipart/alternative; boundary="=_covey_`) {
		t.Fatalf("not multipart:\n%s", head)
	}
	text := strings.Index(body, "Content-Type: text/plain; charset=utf-8")
	html := strings.Index(body, "Content-Type: text/html; charset=utf-8")
	if text < 0 || html < 0 || text > html {
		t.Fatalf("the parts are missing or in the wrong order (text %d, html %d):\n%s", text, html, body)
	}
	if strings.Count(body, "=C3=A4") != 2 {
		t.Errorf("the umlaut is not quoted-printable in both parts:\n%s", body)
	}
	if !strings.HasSuffix(msg, "--\r\n") {
		t.Error("the multipart is not closed")
	}

	plain := string(build("covey <no-reply@example.test>", "no-reply@example.test", Message{
		To: "erika@example.test", Subject: "hello", Body: "text",
	}, time.Now()))
	if !strings.Contains(plain, "Content-Type: text/plain; charset=utf-8") || strings.Contains(plain, "multipart") {
		t.Errorf("a message without HTML is not plain any more:\n%s", plain)
	}
}

// The HTML part is built from text somebody else may have written — a
// display name, a task title. It is escaped, its addresses become links, and
// a paragraph that is only an address becomes the button.
func TestFromTextEscapesAndLinks(t *testing.T) {
	got := string(FromText("Hello <b>Erika</b>,\n\nopen this:\n\nhttps://covey.example.test/verify?token=a&b\n\nSee https://example.test/x for more.", "Confirm"))
	for _, want := range []string{
		"Hello &lt;b&gt;Erika&lt;/b&gt;,",
		`<a href="https://covey.example.test/verify?token=a&amp;b" style="display:inline-block;`,
		">Confirm</a>",
		`<a href="https://example.test/x" style="color:`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the HTML lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<b>") {
		t.Errorf("markup from the text survived unescaped:\n%s", got)
	}
}

func TestRenderIsSelfContained(t *testing.T) {
	out := Render("de", Page{Site: "covey <test>", Title: "Title", Body: Paragraph("x"), Footer: Paragraph("y")})
	if !strings.Contains(out, "covey &lt;test&gt;") || !strings.Contains(out, `<html lang="de">`) {
		t.Errorf("the layout does not escape or does not carry the language:\n%s", out)
	}
	// Nothing remote: no font, no image, no stylesheet to fetch.
	for _, forbidden := range []string{"<link", "<img", "@import", "url("} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the layout loads something remote (%s):\n%s", forbidden, out)
		}
	}
}
