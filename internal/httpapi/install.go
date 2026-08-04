package httpapi

// Das Installationsskript, ausgeliefert von der Instanz selbst:
//
//	curl -sSL https://covey.example/install.sh | sh
//
// Der Nutzen gegenüber dem Weg über GitHub ist die Version: Die Instanz kennt
// ihre eigene und gibt sie dem Skript vor. Ein Runner, der so installiert
// wird, passt damit zum Server, bei dem er sich anmeldet — die Protokoll-Drift
// aus spec/16 kann gar nicht erst entstehen.
//
// Ohne Anmeldung erreichbar, und das ist kein Versehen: Wer das Skript
// abruft, hat naturgemäß noch keinen Zugang. Preisgegeben wird dabei nichts,
// was nicht ohnehin öffentlich ist — der Rumpf steht im Repository, die
// Version im Fuß der Oberfläche und unter /api/v1/version.

import (
	"net/http"

	"covey/installer"
	"covey/internal/buildinfo"
)

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	// Die Voreinstellung hängt am Weg: Wer das Skript bei einer laufenden
	// Instanz abholt, hat den Server schon — er will fast immer einen Runner
	// dazustellen. Über GitHub gilt weiterhin "server". Die Frage am Terminal
	// bleibt davon unberührt, das hier ist nur die Antwort für den Fall ohne
	// Terminal.
	standard := "runner"
	if q := r.URL.Query().Get("default"); q == "server" || q == "runner" || q == "all" {
		standard = q
	}

	skript := installer.Render(installer.VersionFuerRelease(buildinfo.Get().Version), standard)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	// Kein Caching: Nach einem Upgrade der Instanz muss das Skript sofort die
	// neue Version nennen, sonst installiert es tagelang die alte.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(skript))
}
