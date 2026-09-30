package voice

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

/* Speech is how an agent carrying the voice sounds when a call speaks its
 * words (#497). There is one source of covey's voices: the organisation's
 * voice provider, an OpenAI-compatible speech server (educa AI by default).
 * So this is what that server is told — which of its voices, a short style
 * hint, and the speed. The spoken voice belongs to how the agent speaks, so
 * it sits on the voice, next to the chat tone, and follows the same
 * resolution per occasion (#471): the chat voice in effect for a
 * conversation is the one whose speech the call uses. Set, not built:
 * nothing of it is measured from texts. Unset, the provider's default voice
 * speaks, with a style derived from the chat tone. */
type Speech struct {
	// Voice is the voice's name at the provider; empty uses the
	// organisation's default voice.
	Voice string `json:"voice,omitempty"`
	// Instructions is a short English style hint the provider is told
	// ("calm and warm, a little slower"); empty derives one from the chat
	// tone (SpeechInstructions).
	Instructions string `json:"instructions,omitempty"`
	// Speed is the speaking speed, 1 the voice's own; 0 leaves it at that.
	Speed float64 `json:"speed,omitempty"`
}

// Bounds of the speed: the range an OpenAI-compatible provider honours
// (educa AI clamps to it); faster than that is not understood in a call.
const (
	MinSpeed = 0.7
	MaxSpeed = 1.3
)

// ProviderNameMax bounds a provider's model and voice names.
const ProviderNameMax = 100

// InstructionsMax bounds the style hint, and what the synthesize endpoint
// takes.
const InstructionsMax = 300

// Normalized trims the speech and refuses what the provider could not
// take. Nothing set means unset: nil is returned.
func (s Speech) Normalized() (*Speech, error) {
	out := Speech{
		Voice:        strings.TrimSpace(s.Voice),
		Instructions: strings.Join(strings.Fields(s.Instructions), " "),
		Speed:        s.Speed,
	}
	if out.Speed != 0 && (out.Speed < MinSpeed || out.Speed > MaxSpeed) {
		return nil, fmt.Errorf("%w: speed is between %.1f and %.1f (or 0 for the voice's own)", ErrInvalid, MinSpeed, MaxSpeed)
	}
	if utf8.RuneCountInString(out.Voice) > ProviderNameMax {
		return nil, fmt.Errorf("%w: the voice name is at most %d characters", ErrInvalid, ProviderNameMax)
	}
	if utf8.RuneCountInString(out.Instructions) > InstructionsMax {
		return nil, fmt.Errorf("%w: the instructions are at most %d characters", ErrInvalid, InstructionsMax)
	}
	if out == (Speech{}) {
		return nil, nil
	}
	return &out, nil
}

// SpeechInstructions is the short English line the voice provider is told
// how to speak with when the voice sets none: the register of the chat tone
// in effect, and the voice's language when it is known.
func SpeechInstructions(tone ChatTone, language string) string {
	var out string
	switch tone.Tone {
	case ToneCasual:
		out = "casual and friendly, concise"
	case ToneFormal:
		out = "polite and formal, concise"
	default:
		out = "friendly and matter-of-fact, concise"
	}
	if name := languageName(language); name != "" {
		out += "; speak " + name
	}
	return out
}

// languageName names the languages voices are built in; others are left to
// the text.
func languageName(tag string) string {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	return map[string]string{
		"de": "German", "en": "English", "es": "Spanish", "fr": "French", "it": "Italian",
		"nl": "Dutch", "pl": "Polish", "pt": "Portuguese", "ja": "Japanese", "zh": "Chinese",
	}[base]
}
