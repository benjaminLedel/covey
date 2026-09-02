package mail

// The mails' texts come out of the interface's own language catalogues
// (web/src/locales/*.json, embedded through package web).
//
// Not a second set of templates in Go, for a reason the repository already has
// a test for: ten catalogues have to carry the same keys
// (web/src/locales/parity.test.ts). A mail text kept next to them in Go would
// be outside that check — and the first thing to fall out of step is a
// sentence nobody sees in the interface.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"covey/web"
)

// BaseLang is the fallback, as in the frontend (web/src/i18n.ts). A catalogue
// that does not exist, or a key an old translation does not have yet, falls
// back to English rather than showing the key.
const BaseLang = "en"

var (
	once      sync.Once
	catalogs  map[string]map[string]any
	loadError error
)

func load() {
	catalogs = map[string]map[string]any{}
	dir, err := web.Locales()
	if err != nil {
		loadError = err
		return
	}
	entries, err := fs.ReadDir(dir, ".")
	if err != nil {
		loadError = err
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := fs.ReadFile(dir, name)
		if err != nil {
			loadError = err
			return
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			loadError = fmt.Errorf("%s: %w", name, err)
			return
		}
		catalogs[strings.TrimSuffix(name, ".json")] = doc
	}
}

// lookup walks a dotted key through one catalogue.
func lookup(cat map[string]any, key string) (string, bool) {
	var cur any = cat
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = m[part]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

// Text renders one catalogue entry: the language if it has the key, English
// otherwise, and the key itself if even English does not have it — that last
// case is a bug, and a visible key says so louder than an empty line.
//
// The placeholders are i18next's, {{name}}, so the same string works in the
// interface and in a mail without a second syntax.
func Text(lang, key string, vars map[string]string) string {
	once.Do(load)
	s, ok := "", false
	if cat, exists := catalogs[lang]; exists {
		s, ok = lookup(cat, key)
	}
	if !ok || strings.TrimSpace(s) == "" {
		if cat, exists := catalogs[BaseLang]; exists {
			s, ok = lookup(cat, key)
		}
	}
	if !ok {
		return key
	}
	for name, value := range vars {
		s = strings.ReplaceAll(s, "{{"+name+"}}", value)
	}
	return s
}

// Lang normalises what a browser sends ("de-DE", "DE", " de ") to a catalogue
// this binary carries; anything unknown becomes the base language.
func Lang(raw string) string {
	once.Do(load)
	l := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	if _, ok := catalogs[l]; ok {
		return l
	}
	return BaseLang
}
