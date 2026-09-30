package speech

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Every voice in the catalogue says what the app needs to load it and what
// a person needs to know about it (#497): its family, language, speakers,
// licence and source, and a single archive pinned by digest on the host the
// other small models come from.
func TestTheVoiceCatalogueIsComplete(t *testing.T) {
	names := VoiceNames()
	if len(names) == 0 {
		t.Fatal("the catalogue holds no voice")
	}
	for _, n := range names {
		m := Models[n]
		v := m.Voice
		if m.Name != n || m.Engine != EngineTTS || v == nil {
			t.Fatalf("%s: name %q, engine %q, voice %v", n, m.Name, m.Engine, v)
		}
		if v.Family != "vits" {
			t.Errorf("%s: family %q is not one the app loads", n, v.Family)
		}
		if !regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`).MatchString(v.Language) {
			t.Errorf("%s: language %q is not BCP 47", n, v.Language)
		}
		if v.Speakers < 1 || v.Label == "" || v.Licence == "" || !strings.HasPrefix(v.Source, "https://") || m.Credit == "" {
			t.Errorf("%s: speakers %d, label %q, licence %q, source %q, credit %q", n, v.Speakers, v.Label, v.Licence, v.Source, m.Credit)
		}
		if m.Unpack != UnpackTarBz2 || len(m.Files) != 1 {
			t.Fatalf("%s: unpack %q with %d files, want one tar.bz2", n, m.Unpack, len(m.Files))
		}
		f := m.Files[0]
		if !strings.HasSuffix(f.Name, ".tar.bz2") || f.Size <= 0 || !hexDigest.MatchString(f.SHA256) {
			t.Errorf("%s: file %+v", n, f)
		}
		if !strings.HasPrefix(f.URL, "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/") ||
			!strings.HasSuffix(f.URL, "/"+f.Name) {
			t.Errorf("%s: %s is not sherpa-onnx's release of %s", n, f.URL, f.Name)
		}
		if m.Digest() != f.SHA256 {
			t.Errorf("%s: a single archive is kept under its own digest", n)
		}
	}
}

// Until the voices are chosen, the catalogue's one entry is a placeholder.
func TestThePlaceholderIsMarked(t *testing.T) {
	v := Models["piper-en-norman"].Voice
	if v == nil || !v.Placeholder {
		t.Fatal("the placeholder voice must say it is one")
	}
}

// Voices are offered only when the operator names them, and never among
// the recognition models an older app lists.
func TestVoicesAreOfferedApartFromTheModels(t *testing.T) {
	set, err := NewSet("parakeet", nil, nil, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Voices) != 0 {
		t.Fatalf("voices offered without being named: %v", set.Voices)
	}
	if _, err := set.Get("piper-en-norman"); err == nil {
		t.Fatal("a voice not named must not be served")
	}

	set, err = NewSet("parakeet", nil, []string{" piper-en-norman", "piper-en-norman"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(set.Voices, []string{"piper-en-norman"}) {
		t.Fatalf("voices = %v", set.Voices)
	}
	if slices.Contains(set.Names, "piper-en-norman") {
		t.Fatalf("a voice among the models: %v", set.Names)
	}
	if st, err := set.Get("piper-en-norman"); err != nil || st.Model.Voice == nil {
		t.Fatalf("the named voice is served: %v", err)
	}

	if _, err := NewSet("parakeet", nil, []string{"sensevoice"}, t.TempDir(), nil); err == nil {
		t.Fatal("a recogniser named as a voice was accepted")
	}
	if _, err := NewSet("parakeet", []string{"piper-en-norman"}, nil, t.TempDir(), nil); err == nil {
		t.Fatal("a voice named as a recogniser was accepted")
	}
	if _, err := NewSet("parakeet", nil, []string{"nobody"}, t.TempDir(), nil); err == nil {
		t.Fatal("an unknown voice was accepted")
	}
}
