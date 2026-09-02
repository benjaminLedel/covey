// Package web bettet das gebaute Frontend (dist/) ins Binary ein (spec/10).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist liefert das dist-Verzeichnis als FS für den SPA-Handler.
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// Die Sprachkataloge als Quelltext, nicht als Bündel.
//
// Sie liegen schon in dist/, aber dort in JavaScript-Brocken, die nur ein
// Browser wieder auseinandernimmt. Der Server braucht sie im Klartext, seit er
// selbst Mails schreibt (#168): eine Bestätigungsmail soll in der Sprache
// ankommen, in der jemand sich registriert hat.
//
// Damit bleibt es bei EINEM Ort je Übersetzung — der Paritätstest
// (src/locales/parity.test.ts) deckt die Mailtexte mit ab, statt dass ein
// zweiter Satz Texte in Go danebenliegt und auseinanderdriftet.
//
//go:embed src/locales/*.json
var localeFS embed.FS

// Locales liefert die Sprachkataloge (de.json, en.json …).
func Locales() (fs.FS, error) {
	return fs.Sub(localeFS, "src/locales")
}
