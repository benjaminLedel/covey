// Package installer trägt das Installationsskript und liefert es so aus, wie
// die laufende Instanz es braucht.
//
// Warum eine Instanz ihr eigenes Installationsskript ausliefert: Sie kennt
// ihre Version. Ein Runner, der darüber installiert wird, passt damit zum
// Server, an dem er sich anmeldet — die Protokoll-Drift aus spec/16 kann gar
// nicht erst entstehen. Für „noch einen Knoten wie diesen" gilt dasselbe.
//
// Die Binaries kommen weiterhin von den GitHub-Releases, nicht von der
// Instanz. Das ist Absicht: Der Vertrauensanker für ausführbaren Code bleibt
// die Projektquelle, auch wenn das Skript von woanders kommt. Die Instanz
// bestimmt die Version, nicht den Inhalt.
package installer

import (
	_ "embed"
	"fmt"
	"strings"
)

// Script ist das Skript, wie es auch auf GitHub liegt (installer/install.sh).
// Eine zweite Kopie im Repo gäbe es nicht ohne Drift — deshalb genau diese
// Datei, eingebettet.
//
//go:embed install.sh
var Script string

// Render setzt der eingebetteten Fassung einen Vorspann voran, der die
// Voreinstellungen der Instanz trägt. Das Skript liest sie über `:=`-Defaults,
// bleibt also unverändert lauffähig, wenn es direkt von GitHub kommt.
//
// version leer = kein Vorspann für die Version: Dann ermittelt das Skript die
// neueste Veröffentlichung selbst. Das ist der richtige Fallback für einen
// Entwicklungs-Build, dessen "dev" es auf GitHub nie geben wird.
func Render(version, standard string) string {
	if standard == "" {
		standard = "server"
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Vorspann dieser Covey-Instanz — der Rumpf ist unverändert das Skript\n")
	b.WriteString("# aus dem Repository (installer/install.sh).\n")
	if version != "" {
		fmt.Fprintf(&b, "COVEY_INSTALL_VERSION=%q\n", version)
		b.WriteString("export COVEY_INSTALL_VERSION\n")
	}
	fmt.Fprintf(&b, "COVEY_INSTALL_DEFAULT=%q\n", standard)
	b.WriteString("export COVEY_INSTALL_DEFAULT\n\n")
	b.WriteString(Script)
	return b.String()
}

// VersionFuerRelease filtert Versionen heraus, zu denen es kein Release geben
// kann. Ein Binary aus `make build` trägt "dev" oder eine `git describe`-
// Fassung mit Commit-Zusatz; würde die Instanz das als feste Version
// vorgeben, liefe das Skript in einen 404 statt in die neueste
// Veröffentlichung.
func VersionFuerRelease(v string) string {
	if !strings.HasPrefix(v, "v") || strings.Contains(v, "dirty") {
		return ""
	}
	// `git describe` hängt bei Commits nach dem Tag "-<n>-g<hash>" an. Auch das
	// ist kein Release-Tag.
	if strings.Count(v, "-") > 0 && !strings.Contains(v, "-rc") {
		return ""
	}
	return v
}
