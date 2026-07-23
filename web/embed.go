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
