package httpapi

// The install script, served by the instance itself:
//
//	curl -sSL https://covey.example/install.sh | sh
//
// The advantage over the route via GitHub is the version: the instance knows
// its own and hands it to the script. A runner installed this way therefore
// matches the server it registers with — the protocol drift from spec/16
// cannot arise in the first place.
//
// Reachable without authentication, and that is no oversight: whoever fetches
// the script naturally has no access yet. Nothing is disclosed that is not
// public anyway — the body lives in the repository, the version in the footer
// of the UI and under /api/v1/version.

import (
	"net/http"

	"covey/installer"
	"covey/internal/buildinfo"
)

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	// The default depends on the route: whoever picks the script up from a
	// running instance already has the server — they almost always want to add
	// a runner. Via GitHub "server" still applies. The prompt in the terminal
	// is unaffected by this; this is only the answer for the case without a
	// terminal.
	standard := "runner"
	if q := r.URL.Query().Get("default"); q == "server" || q == "runner" || q == "all" {
		standard = q
	}

	script := installer.Render(installer.VersionFuerRelease(buildinfo.Get().Version), standard)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	// No caching: after an upgrade of the instance the script must name the new
	// version immediately, otherwise it installs the old one for days.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}
