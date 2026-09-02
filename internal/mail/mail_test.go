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
