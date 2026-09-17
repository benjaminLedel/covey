// Package web embeds the built frontend (dist/) into the binary (spec/10).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the dist directory as an FS for the SPA handler.
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// The language catalogues as source text, not as a bundle.
//
// They are already in dist/, but there in JavaScript chunks that only a
// browser takes apart again. The server needs them in the clear since it
// writes mails itself (#168): a confirmation mail should arrive in the
// language someone signed up in.
//
// That keeps it at ONE place per translation: the parity test
// (src/locales/parity.test.ts) covers the mail texts as well, instead of a
// second set of texts lying next to them in Go and drifting apart.
//
//go:embed src/locales/*.json
var localeFS embed.FS

// Locales returns the language catalogues (de.json, en.json …).
func Locales() (fs.FS, error) {
	return fs.Sub(localeFS, "src/locales")
}
