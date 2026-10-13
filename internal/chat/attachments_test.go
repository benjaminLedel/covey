package chat

import (
	"strings"
	"testing"
)

func TestAttachmentName(t *testing.T) {
	for in, want := range map[string]string{
		"post.png":                        "post.png",
		"../../etc/passwd":                "passwd",
		`C:\Users\ada\a.pdf`:              "a.pdf",
		"":                                "file",
		"..":                              "file",
		"bad\x00name\n.txt":               "badname.txt",
		strings.Repeat("x", 300) + ".png": strings.Repeat("x", 196) + ".png",
	} {
		if got := AttachmentName(in); got != want {
			t.Errorf("AttachmentName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAttachmentMIME(t *testing.T) {
	if got := AttachmentMIME("a.PNG", nil); got != "image/png" {
		t.Errorf("by extension: %q", got)
	}
	if got := AttachmentMIME("ohne", []byte("%PDF-1.7 …")); got != "application/pdf" {
		t.Errorf("by content: %q", got)
	}
}

// TestForAgent: the agent reads where the files lie; the person's text
// stays what they wrote.
func TestForAgent(t *testing.T) {
	m := Message{Text: "Bitte lesen."}
	if m.ForAgent() != "Bitte lesen." {
		t.Fatalf("without files, the text: %q", m.ForAgent())
	}
	m.Attachments = []Attachment{{Name: "a.pdf", HomePath: "chat/1234abcd/a.pdf"}, {Name: "b.png", HomePath: "chat/1234abcd/b.png"}}
	want := "Bitte lesen.\n\n(Attached, in your home: chat/1234abcd/a.pdf, chat/1234abcd/b.png)"
	if got := m.ForAgent(); got != want {
		t.Fatalf("got %q", got)
	}
	m.Text = ""
	if got := m.ForAgent(); !strings.HasPrefix(got, "(Attached") {
		t.Fatalf("files alone: %q", got)
	}
	// An agent's own file has no home path and adds nothing.
	if got := (Message{Text: "Hier.", Attachments: []Attachment{{Name: "x.png"}}}).ForAgent(); got != "Hier." {
		t.Fatalf("an agent's file: %q", got)
	}
}

func TestCheckAttachments(t *testing.T) {
	if CheckAttachments(MaxAttachments, MaxAttachmentBytes) != nil {
		t.Fatal("at the limit is allowed")
	}
	if CheckAttachments(MaxAttachments+1, 0) != ErrTooManyAttachments {
		t.Fatal("too many")
	}
	if CheckAttachments(1, MaxAttachmentBytes+1) != ErrAttachmentTooLarge {
		t.Fatal("too large")
	}
}
