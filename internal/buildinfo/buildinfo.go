// Package buildinfo trägt die Herkunft des laufenden Binaries: Version,
// Commit-Hash und Bauzeit. Beides Binaries (covey, coveyd) nutzen dasselbe
// Paket, damit „welcher Stand läuft hier?" überall dieselbe Antwort hat —
// im Log, auf der CLI, in der API und im Fuß der UI.
//
// Die Werte setzt der Build per -ldflags (siehe Makefile/Dockerfile/CI):
//
//	go build -ldflags "-X covey/internal/buildinfo.version=v0.1.0 \
//	                   -X covey/internal/buildinfo.commit=abc1234 \
//	                   -X covey/internal/buildinfo.date=2026-07-30T12:00:00Z"
//
// Fehlen sie — etwa bei einem nackten `go build ./...` — fällt das Paket auf
// die von Go selbst eingebetteten VCS-Angaben zurück. Im Container-Build gibt
// es die nicht (`.git` liegt in .dockerignore), deshalb reicht dort das
// Makefile/CI die Werte durch.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

// SourceURL ist die öffentliche Quelle dieses Programms. Covey steht unter der
// AGPL-3.0 und läuft als Netzwerkdienst: Wer eine veränderte Fassung anderen
// anbietet, schuldet ihnen den Quelltext. Die Adresse gehört deshalb dorthin,
// wo sie ohne Suche zu finden ist — CLI und UI-Fuß. Wer forkt und betreibt,
// trägt hier seine eigene Adresse ein.
const SourceURL = "https://github.com/benjaminLedel/covey"

// Per -ldflags gesetzt. Kleingeschrieben: der Zugriff läuft über Get().
var (
	version string
	commit  string
	date    string
)

// Info ist die Herkunft des Binaries, so wie API und CLI sie zeigen.
type Info struct {
	// Version ist der Git-Tag bzw. `git describe`; "dev", wenn unbekannt.
	Version string `json:"version"`
	// Commit ist der kurze Commit-Hash; leer, wenn unbekannt.
	Commit string `json:"commit"`
	// BuiltAt ist die Bauzeit in RFC3339 (UTC); leer, wenn unbekannt.
	BuiltAt string `json:"built_at"`
	// Dirty markiert einen Build aus einem Arbeitsbaum mit uncommitteten
	// Änderungen (nur erkennbar, wenn Go die VCS-Infos eingebettet hat).
	Dirty bool `json:"dirty"`
	// Go ist die Toolchain-Version, mit der gebaut wurde.
	Go string `json:"go"`
	// Source ist die öffentliche Quelle (SourceURL). Sie geht mit über die
	// API, damit die UI sie nicht hartkodiert — ein Fork zeigt so seine
	// eigene Adresse, nicht die des Ursprungs.
	Source string `json:"source"`
}

// Get liefert die Herkunft des Binaries (einmal ermittelt, dann gecacht).
var Get = sync.OnceValue(func() Info {
	i := Info{Version: version, Commit: commit, BuiltAt: date, Go: runtime.Version(), Source: SourceURL}

	// Lücken aus den eingebetteten VCS-Angaben füllen (lokaler Build im
	// Git-Arbeitsbaum). Gesetzte -ldflags-Werte gewinnen immer.
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if i.Commit == "" {
					i.Commit = s.Value
				}
			case "vcs.time":
				if i.BuiltAt == "" {
					i.BuiltAt = s.Value
				}
			case "vcs.modified":
				i.Dirty = s.Value == "true"
			}
		}
	}

	if len(i.Commit) > 12 {
		i.Commit = i.Commit[:12]
	}
	if i.Version == "" {
		i.Version = "dev"
	}
	return i
})

// String ist die einzeilige Fassung für Log und CLI:
// "v0.1.0 (abc1234, 2026-07-30T12:00:00Z, go1.26)".
func (i Info) String() string {
	var parts []string
	if i.Commit != "" {
		c := i.Commit
		if i.Dirty {
			c += "-dirty"
		}
		parts = append(parts, c)
	}
	if i.BuiltAt != "" {
		parts = append(parts, i.BuiltAt)
	}
	if i.Go != "" {
		parts = append(parts, i.Go)
	}
	if len(parts) == 0 {
		return i.Version
	}
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(parts, ", "))
}

// String ist die Kurzfassung des laufenden Binaries — für Log-Zeilen.
func String() string { return Get().String() }
