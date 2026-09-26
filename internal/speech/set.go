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
	Names  []string
	stores map[string]*Store
}

// NewSet returns the set, or nil when speech is off (def "off" or empty).
// The default is always offered, whether or not allowed names it.
func NewSet(def string, allowed []string, dataDir string, log *slog.Logger) (*Set, error) {
	if def == "" || def == "off" {
		return nil, nil
	}
	names := []string{def}
	// The speakers' model rides along with any speech model (#367); the app
	// does not list it as a choice.
	allowed = append(append([]string{}, allowed...), SpeakerModel)
	for _, n := range allowed {
		n = strings.TrimSpace(n)
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
		set.Names = append(set.Names, st.Model.Name)
	}
	return set
}
