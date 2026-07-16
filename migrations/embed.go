// Package migrations bettet die versionierten SQL-Migrationen ins Binary ein.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
