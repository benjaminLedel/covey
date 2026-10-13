package httpapi

import (
	"testing"

	"covey/internal/chat"
)

func TestUniqueNames(t *testing.T) {
	files := []chat.Attachment{{Name: "a.png"}, {Name: "A.png"}, {Name: "a.png"}, {Name: "b"}, {Name: "b"}}
	uniqueNames(files)
	want := []string{"a.png", "A (2).png", "a (3).png", "b", "b (2)"}
	for i, f := range files {
		if f.Name != want[i] {
			t.Errorf("%d: %q, want %q", i, f.Name, want[i])
		}
	}
}

func TestChatInlineType(t *testing.T) {
	for name, want := range map[string]string{
		"a.png": "image/png", "a.pdf": "application/pdf", "a.MP4": "video/mp4",
		"a.webm": "video/webm", "a.html": "", "a.svg": "image/svg+xml", "a": "",
	} {
		if got := chatInlineType(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}
