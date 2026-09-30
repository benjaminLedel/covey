package voice

import (
	"fmt"
	"strings"

	"covey/internal/speech"
)

/* Speech is how an agent carrying the voice sounds when its words are spoken
 * (#497): in a call, the Mac app speaks the agent's replies on the device.
 * The spoken voice belongs to how the agent speaks, so it sits on the voice,
 * next to the chat tone, and follows the same resolution per occasion (#471):
 * the chat voice in effect for a conversation is the one whose speech the
 * app uses. Set, not built: nothing of it is measured from texts.
 *
 * Two sources: a voice of the instance's catalogue, synthesised on the
 * device, or the organisation's own speech server. Unset, the app picks a
 * voice per agent itself. */
type Speech struct {
	// Source: SourceDevice (a voice of the speech catalogue, synthesised on
	// the device) or SourceServer (the organisation's own speech server,
	// reached through the control plane).
	Source string `json:"source"`
	// Model names the catalogue voice for the device (speech.Models, engine
	// "tts"), or the speech server's model.
	Model string `json:"model,omitempty"`
	// Voice is the speech server's voice name; empty on the device.
	Voice string `json:"voice,omitempty"`
	// Speaker picks one of a device model's speakers, from 0.
	Speaker int `json:"speaker"`
	// Rate is the speaking rate, 1 the model's own; 0 means 1.
	Rate float64 `json:"rate,omitempty"`
}

// The sources, as the API spells them. There are two on purpose: the
// device, and a server the organisation runs itself — no speech leaves for a
// cloud service billed per character.
const (
	SourceDevice = "device"
	SourceServer = "server"
)

// Bounds of the rate: slower than half is dragging, faster than double not
// understood.
const (
	MinRate = 0.5
	MaxRate = 2.0
)

// ServerNameMax bounds a speech server's model and voice names.
const ServerNameMax = 100

// Normalized trims the speech and refuses what the app could not speak.
// An empty source means unset: nil is returned.
func (s Speech) Normalized() (*Speech, error) {
	out := Speech{
		Source:  strings.ToLower(strings.TrimSpace(s.Source)),
		Model:   strings.TrimSpace(s.Model),
		Voice:   strings.TrimSpace(s.Voice),
		Speaker: s.Speaker,
		Rate:    s.Rate,
	}
	if out.Rate != 0 && (out.Rate < MinRate || out.Rate > MaxRate) {
		return nil, fmt.Errorf("%w: rate is between %.1f and %.1f (or 0 for the model's own)", ErrInvalid, MinRate, MaxRate)
	}
	switch out.Source {
	case "":
		if out.Model != "" || out.Voice != "" {
			return nil, fmt.Errorf("%w: a model or voice needs a source, %q or %q", ErrInvalid, SourceDevice, SourceServer)
		}
		return nil, nil
	case SourceDevice:
		m, ok := speech.Models[out.Model]
		if !ok || m.Engine != speech.EngineTTS || m.Voice == nil {
			return nil, fmt.Errorf("%w: model %q is not a voice of the catalogue (%s)", ErrInvalid, out.Model, strings.Join(speech.VoiceNames(), ", "))
		}
		if out.Speaker < 0 || out.Speaker >= m.Voice.Speakers {
			return nil, fmt.Errorf("%w: %s has speakers 0 to %d", ErrInvalid, out.Model, m.Voice.Speakers-1)
		}
		if out.Voice != "" {
			return nil, fmt.Errorf("%w: a voice name is the speech server's; on the device the speaker picks", ErrInvalid)
		}
	case SourceServer:
		if out.Model == "" {
			return nil, fmt.Errorf("%w: the speech server needs the name of its model", ErrInvalid)
		}
		if len([]rune(out.Model)) > ServerNameMax || len([]rune(out.Voice)) > ServerNameMax {
			return nil, fmt.Errorf("%w: model and voice are at most %d characters", ErrInvalid, ServerNameMax)
		}
		if out.Speaker != 0 {
			return nil, fmt.Errorf("%w: the speech server's voice is named, not numbered", ErrInvalid)
		}
	default:
		return nil, fmt.Errorf("%w: source is %q or %q", ErrInvalid, SourceDevice, SourceServer)
	}
	return &out, nil
}

// SpeechInstructions is the short English line a speech server that takes
// instructions (educa AI's /v1/audio/speech) is told how to speak with: the
// register of the chat tone in effect, and the voice's language when it is
// known. The app passes it back when it asks for speech.
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
