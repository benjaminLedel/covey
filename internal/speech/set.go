package speech

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// Set is the models an instance offers (#351): one default, which is
// prepared at startup, and the others the operator allows, fetched the first
// time somebody picks one in the app. A larger model recognises better and
// costs download, disk and time on the phone — which trade-off fits is the
// person's call, within what the operator is willing to host.
type Set struct {
	Default string
	// Names in the order the app lists them: smallest first.
	Names []string
	// Voices are the speech synthesis models offered (#497), sorted by
	// name. They are not among Names: an app that knows only recognition
	// models must not list a voice as one.
	Voices []string
	stores map[string]*Store
}

// NewSet returns the set, or nil when speech is off (def "off" or empty).
// The default is always offered, whether or not allowed names it. voices
// are the voices the operator offers (COVEY_SPEECH_VOICES); each is fetched
// the first time an app asks for it.
func NewSet(def string, allowed, voices []string, dataDir string, log *slog.Logger) (*Set, error) {
	if def == "" || def == "off" {
		return nil, nil
	}
	names := []string{def}
	// The speakers' model (#367) and the voice activity detector (#494)
	// ride along with any speech model; the app does not list them as a
	// choice.
	allowed = append(append([]string{}, allowed...), SpeakerModel, VADModel)
	for _, n := range allowed {
		n = strings.TrimSpace(n)
		if Models[n].Engine == EngineTTS {
			return nil, fmt.Errorf("COVEY_SPEECH_MODELS: %q is a voice; name it in COVEY_SPEECH_VOICES", n)
		}
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	set := &Set{Default: def, stores: map[string]*Store{}}
	for _, n := range names {
		st, err := New(n, dataDir, log)
		if err != nil {
			return nil, err
		}
		set.stores[n] = st
	}
	for _, n := range voices {
		n = strings.TrimSpace(n)
		if n == "" || slices.Contains(set.Voices, n) {
			continue
		}
		if m, ok := Models[n]; !ok || m.Engine != EngineTTS {
			return nil, fmt.Errorf("COVEY_SPEECH_VOICES: %q is not a voice of the catalogue (%s)", n, strings.Join(VoiceNames(), ", "))
		}
		st, err := New(n, dataDir, log)
		if err != nil {
			return nil, err
		}
		set.stores[n] = st
		set.Voices = append(set.Voices, n)
	}
	slices.Sort(set.Voices)
	set.Names = names
	slices.SortFunc(set.Names, func(a, b string) int {
		switch da, db := Models[a].Size(), Models[b].Size(); {
		case da < db:
			return -1
		case da > db:
			return 1
		}
		return 0
	})
	return set, nil
}

// Get returns the named model's store; an empty name is the default.
func (s *Set) Get(name string) (*Store, error) {
	if name == "" {
		name = s.Default
	}
	st, ok := s.stores[name]
	if !ok {
		return nil, fmt.Errorf("speech model %q is not offered here", name)
	}
	return st, nil
}

// SetOf offers exactly the given stores, the first as the default — for
// tests, and for callers that build their stores themselves.
func SetOf(stores ...*Store) *Set {
	set := &Set{Default: stores[0].Model.Name, stores: map[string]*Store{}}
	for _, st := range stores {
		set.stores[st.Model.Name] = st
		if st.Model.Engine == EngineTTS {
			set.Voices = append(set.Voices, st.Model.Name)
		} else {
			set.Names = append(set.Names, st.Model.Name)
		}
	}
	return set
}
