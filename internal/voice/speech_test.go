package voice

import (
	"errors"
	"strings"
	"testing"
)

// The spoken voice (#497) takes only what the voice provider can: a voice
// name, a short style hint, and a speed it honours.
func TestSpeechNormalized(t *testing.T) {
	ok := []Speech{
		{Voice: " alloy ", Speed: 1.1},
		{Voice: "DEFAULT_VOICE", Speed: 1.3},
		{Instructions: "  calm and\n warm  "},
		{Speed: 0.7},
	}
	for _, in := range ok {
		out, err := in.Normalized()
		if err != nil || out == nil {
			t.Fatalf("%+v refused: %v", in, err)
		}
	}
	if out, _ := ok[0].Normalized(); out.Voice != "alloy" {
		t.Fatalf("voice not trimmed: %+v", out)
	}
	if out, _ := ok[2].Normalized(); out.Instructions != "calm and warm" {
		t.Fatalf("instructions not folded: %+v", out)
	}
	for _, empty := range []Speech{{}, {Voice: "  ", Instructions: " \n "}} {
		if out, err := empty.Normalized(); out != nil || err != nil {
			t.Fatalf("empty is unset: %+v %v", out, err)
		}
	}
	bad := []Speech{
		{Voice: strings.Repeat("x", ProviderNameMax+1)},
		{Instructions: strings.Repeat("i", InstructionsMax+1)},
		{Voice: "alloy", Speed: 0.5},
		{Voice: "alloy", Speed: 2},
		{Speed: -1},
	}
	for _, in := range bad {
		if _, err := in.Normalized(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v accepted: %v", in, err)
		}
	}
}

// The line the provider is told how to speak with follows the chat tone.
func TestSpeechInstructions(t *testing.T) {
	for _, c := range []struct {
		tone ChatTone
		lang string
		want string
	}{
		{ChatTone{}, "", "friendly and matter-of-fact, concise"},
		{ChatTone{Tone: ToneCasual}, "de", "casual and friendly, concise; speak German"},
		{ChatTone{Tone: ToneFormal}, "en-GB", "polite and formal, concise; speak English"},
		{ChatTone{Tone: ToneMatterOfFact}, "tlh", "friendly and matter-of-fact, concise"},
	} {
		if got := SpeechInstructions(c.tone, c.lang); got != c.want {
			t.Errorf("%+v %q = %q, want %q", c.tone, c.lang, got, c.want)
		}
		if len([]rune(SpeechInstructions(c.tone, c.lang))) > InstructionsMax {
			t.Error("longer than the synthesize endpoint takes")
		}
	}
}
