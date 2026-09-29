package voice

import (
	"errors"
	"strings"
	"testing"
)

// TestChatToneNormalized: values are trimmed and lower-cased, anything else
// is the caller's mistake, and the free text is a line.
func TestChatToneNormalized(t *testing.T) {
	got, err := ChatTone{Address: " Du ", Tone: "CASUAL", Emoji: "sparingly", Note: "  kurz  "}.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != AddressDu || got.Tone != ToneCasual || got.Note != "kurz" {
		t.Fatalf("normalized: %+v", got)
	}
	for _, falsch := range []ChatTone{
		{Address: "ihr"}, {Tone: "rude"}, {Emoji: "always"},
		{Note: strings.Repeat("x", ChatToneNoteMax+1)},
	} {
		if _, err := falsch.Normalized(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v must be refused, got %v", falsch, err)
		}
	}
	if _, err := (ChatTone{}).Normalized(); err != nil {
		t.Fatalf("an empty tone is allowed: %v", err)
	}
}

// TestEffectiveChatTone: the voice wins field by field, the organisation
// fills what it leaves open; auto keeps the organisation's du or Sie as what
// to fall back on.
func TestEffectiveChatTone(t *testing.T) {
	org := ChatTone{Address: AddressSie, Tone: ToneFormal, Emoji: EmojiNever, Note: "Wir sind eine Kanzlei."}
	if got := EffectiveChatTone(ChatTone{}, org); got.Address != AddressSie || got.Note != org.Note {
		t.Fatalf("without a voice tone the organisation's applies: %+v", got)
	}
	got := EffectiveChatTone(ChatTone{Address: AddressAuto, Emoji: EmojiFreely}, org)
	if got.Address != AddressAuto || got.Tone != ToneFormal || got.Emoji != EmojiFreely {
		t.Fatalf("merged: %+v", got)
	}
	p := got.Prompt()
	for _, will := range []string{"How you talk in the team chat", "the way they address you", `"Sie"`, "polite and formal", "emoji are welcome", "Kanzlei"} {
		if !strings.Contains(p, will) {
			t.Errorf("prompt lacks %q: %s", will, p)
		}
	}
}

// TestChatTonePromptLeer: nothing set, nothing said — the turns talk as they
// did before there was a tone.
func TestChatTonePromptLeer(t *testing.T) {
	if p := (ChatTone{}).Prompt(); p != "" {
		t.Fatalf("empty tone: %q", p)
	}
	if p := (ChatTone{Note: "Bitte ohne Anglizismen."}).Prompt(); !strings.HasSuffix(p, "Bitte ohne Anglizismen.") {
		t.Fatalf("a note alone: %q", p)
	}
}
