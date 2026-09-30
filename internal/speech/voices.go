package speech

import "slices"

// Voices the app speaks with (#497): speech synthesis models that run on the
// device, offered the way the recognition models are — pinned by digest,
// fetched once by the instance, served to signed-in apps, fetched once by
// the app and kept there.
//
// The app synthesises through sherpa-onnx's offline TTS, which loads several
// model families; [VoiceInfo.Family] says which one a model is, so a new
// family is a new entry here and a new case in the app, not a new path. The
// catalogue is deliberately short while the voices are chosen: the one entry
// below proves the path end to end and is marked as a placeholder. An entry
// is offered only when the operator names it (COVEY_SPEECH_VOICES); without
// one the app speaks with the system's synthesis.

// EngineTTS marks a model as a voice rather than a recogniser.
const EngineTTS = "tts"

// VoiceInfo describes a speech synthesis model.
type VoiceInfo struct {
	// Family is how sherpa-onnx loads it: "vits" (Piper and other VITS
	// models) for now; "kokoro", "matcha" and the others it knows follow as
	// entries need them.
	Family string `json:"family"`
	// Language is BCP 47, as specific as the model is: "en-US", "de-DE".
	Language string `json:"language"`
	// Speakers is how many voices the model holds; a speaker id picks one.
	Speakers int `json:"speakers"`
	// Label is the voice's name as the app shows it.
	Label string `json:"label"`
	// Licence names the terms the model is redistributed under, as far as
	// its card says: the training data's and the model's own.
	Licence string `json:"licence"`
	// Source is where the model and its card come from.
	Source string `json:"source"`
	// Placeholder marks an entry that is there to exercise the pipeline, not
	// a voice chosen to be offered.
	Placeholder bool `json:"placeholder,omitempty"`
}

// Unpack formats: a model that comes as an archive is served as the
// archive, verified by its digest, and unpacked by the app.
const UnpackTarBz2 = "tar.bz2"

func init() {
	for name, m := range voiceModels {
		m.Name = name
		m.Engine = EngineTTS
		Models[name] = m
	}
}

// voiceModels are the voices the catalogue holds, by name. sherpa-onnx
// publishes its converted models as release archives on GitHub — the host
// the speaker model and the voice detector already come from.
var voiceModels = map[string]Model{
	// A placeholder (#497): the smallest Piper voice trained from scratch on
	// public-domain recordings, int8, 21 MB. Good enough to hear that the
	// path works; not the voice covey will offer.
	"piper-en-norman": {
		Credit: "Piper voice “norman” (en-US) · recordings from LibriVox, public domain",
		Unpack: UnpackTarBz2,
		Voice: &VoiceInfo{
			Family:      "vits",
			Language:    "en-US",
			Speakers:    1,
			Label:       "Norman",
			Licence:     "public domain (LibriVox recordings); trained from scratch",
			Source:      "https://huggingface.co/rhasspy/piper-voices/tree/main/en/en_US/norman/medium",
			Placeholder: true,
		},
		Files: []File{{
			Name:   "vits-piper-en_US-norman-medium-int8.tar.bz2",
			URL:    "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-piper-en_US-norman-medium-int8.tar.bz2",
			SHA256: "cb481a514bc213ccf3899391c0f27fdcc4e4b814ec30496f28089a027b5aa01b",
			Size:   20987233,
		}},
	},
}

// VoiceNames are the voices of the catalogue, sorted.
func VoiceNames() []string {
	var out []string
	for n, m := range Models {
		if m.Engine == EngineTTS {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}
