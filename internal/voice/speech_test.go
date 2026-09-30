package voice

import (
	"errors"
	"strings"
	"testing"
)

// The spoken voice (#497) takes only what the app can speak: a voice of the
// catalogue with one of its speakers on the device, or a model (and voice)
// of the organisation's speech server, at a rate a person can follow.
func TestSpeechNormalized(t *testing.T) {
	ok := []Speech{
		{Source: " Device ", Model: " piper-en-norman ", Rate: 1.1},
		{Source: "device", Model: "piper-en-norman", Rate: 2},
		{Source: "server", Model: "kokoro"},
		{Source: "server", Model: "tts-1", Voice: " alloy ", Rate: 0.5},
	}
	for _, in := range ok {
		out, err := in.Normalized()
		if err != nil || out == nil {
			t.Fatalf("%+v refused: %v", in, err)
		}
	}
	out, _ := ok[0].Normalized()
	if out.Source != SourceDevice || out.Model != "piper-en-norman" {
		t.Fatalf("not trimmed: %+v", out)
	}
	if out, _ := ok[3].Normalized(); out.Voice != "alloy" {
		t.Fatalf("voice not trimmed: %+v", out)
	}
	if out, err := (Speech{}).Normalized(); out != nil || err != nil {
		t.Fatalf("empty is unset: %+v %v", out, err)
	}
	long := strings.Repeat("x", ServerNameMax+1)
	bad := []Speech{
		{Source: "cloud", Model: "tts-1"},
		{Source: "system"},
		{Source: "device"},
		{Source: "device", Model: "parakeet"},
		{Source: "device", Model: "nobody"},
		{Source: "device", Model: "piper-en-norman", Speaker: 1},
		{Source: "device", Model: "piper-en-norman", Speaker: -1},
		{Source: "device", Model: "piper-en-norman", Voice: "alloy"},
		{Source: "server"},
		{Source: "server", Model: long},
		{Source: "server", Model: "tts-1", Voice: long},
		{Source: "server", Model: "tts-1", Speaker: 2},
		{Source: "server", Model: "tts-1", Rate: 0.3},
		{Source: "device", Model: "piper-en-norman", Rate: 2.5},
		{Model: "piper-en-norman"},
		{Voice: "alloy"},
	}
	for _, in := range bad {
		if _, err := in.Normalized(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v accepted: %v", in, err)
		}
	}
}

// The line a speech server is told how to speak with follows the chat tone.
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
		if len([]rune(SpeechInstructions(c.tone, c.lang))) > 300 {
			t.Error("longer than the synthesize endpoint takes")
		}
	}
}
