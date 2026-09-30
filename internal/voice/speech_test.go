package voice

import (
	"errors"
	"testing"
)

// The spoken voice (#497) takes only what the app can speak: a voice of the
// catalogue with one of its speakers, or the system's synthesis, at a rate
// a person can follow.
func TestSpeechNormalized(t *testing.T) {
	ok := []Speech{
		{Engine: " Sherpa-ONNX ", Model: " piper-en-norman ", Rate: 1.1},
		{Engine: "system"},
		{Engine: "system", Rate: 0.5},
		{Engine: "sherpa-onnx", Model: "piper-en-norman", Rate: 2},
	}
	for _, in := range ok {
		out, err := in.Normalized()
		if err != nil || out == nil {
			t.Fatalf("%+v refused: %v", in, err)
		}
	}
	out, _ := ok[0].Normalized()
	if out.Engine != EngineSherpa || out.Model != "piper-en-norman" {
		t.Fatalf("not trimmed: %+v", out)
	}
	if out, err := (Speech{}).Normalized(); out != nil || err != nil {
		t.Fatalf("empty is unset: %+v %v", out, err)
	}
	bad := []Speech{
		{Engine: "espeak"},
		{Engine: "sherpa-onnx"},
		{Engine: "sherpa-onnx", Model: "parakeet"},
		{Engine: "sherpa-onnx", Model: "nobody"},
		{Engine: "sherpa-onnx", Model: "piper-en-norman", Speaker: 1},
		{Engine: "sherpa-onnx", Model: "piper-en-norman", Speaker: -1},
		{Engine: "system", Model: "piper-en-norman"},
		{Engine: "system", Speaker: 2},
		{Engine: "system", Rate: 0.3},
		{Engine: "system", Rate: 2.5},
		{Model: "piper-en-norman"},
	}
	for _, in := range bad {
		if _, err := in.Normalized(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v accepted: %v", in, err)
		}
	}
}
