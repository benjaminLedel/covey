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
 * Unset, the app picks a voice per agent itself. */
type Speech struct {
	// Engine: EngineSherpa (a voice of the speech catalogue, synthesised
	// on the device) or EngineSystem (the device's own synthesis).
	Engine string `json:"engine"`
	// Model names the catalogue voice (speech.Models, engine "tts");
	// empty for the system's synthesis.
	Model string `json:"model,omitempty"`
	// Speaker picks one of the model's speakers, from 0.
	Speaker int `json:"speaker"`
	// Rate is the speaking rate, 1 the model's own; 0 means 1.
	Rate float64 `json:"rate,omitempty"`
}

// The engines, as the API spells them.
const (
	EngineSherpa = "sherpa-onnx"
	EngineSystem = "system"
)

// Bounds of the rate: slower than half is dragging, faster than double not
// understood.
const (
	MinRate = 0.5
	MaxRate = 2.0
)

// Normalized trims the speech and refuses what the app could not speak.
// An empty engine means unset: nil is returned.
func (s Speech) Normalized() (*Speech, error) {
	out := Speech{
		Engine:  strings.ToLower(strings.TrimSpace(s.Engine)),
		Model:   strings.TrimSpace(s.Model),
		Speaker: s.Speaker,
		Rate:    s.Rate,
	}
	if out.Rate != 0 && (out.Rate < MinRate || out.Rate > MaxRate) {
		return nil, fmt.Errorf("%w: rate is between %.1f and %.1f (or 0 for the model's own)", ErrInvalid, MinRate, MaxRate)
	}
	switch out.Engine {
	case "":
		if out.Model != "" {
			return nil, fmt.Errorf("%w: a model needs the engine %q", ErrInvalid, EngineSherpa)
		}
		return nil, nil
	case EngineSystem:
		if out.Model != "" || out.Speaker != 0 {
			return nil, fmt.Errorf("%w: the system's synthesis takes no model and no speaker", ErrInvalid)
		}
	case EngineSherpa:
		m, ok := speech.Models[out.Model]
		if !ok || m.Engine != speech.EngineTTS || m.Voice == nil {
			return nil, fmt.Errorf("%w: model %q is not a voice of the catalogue (%s)", ErrInvalid, out.Model, strings.Join(speech.VoiceNames(), ", "))
		}
		if out.Speaker < 0 || out.Speaker >= m.Voice.Speakers {
			return nil, fmt.Errorf("%w: %s has speakers 0 to %d", ErrInvalid, out.Model, m.Voice.Speakers-1)
		}
	default:
		return nil, fmt.Errorf("%w: engine is %q or %q", ErrInvalid, EngineSherpa, EngineSystem)
	}
	return &out, nil
}
