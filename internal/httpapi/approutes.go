package httpapi

// Which addresses this binary knows, and what it tells a crawler.
//
// Until #130 this file carried a second half: the website lived in the binary,
// so the binary had to produce sitemap.xml and a robots.txt that let the public
// pages through. The website has its own repository and its own host now
// (#129); what is left here answers with the interface or with a 404, and asks
// to be left out of the index entirely.
//
// The list of app paths comes from dist/app-routes.json, which the build writes
// from web/src/public/routes.ts — one source for the routing in the browser and
// the decision here.

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// appRouten is dist/app-routes.json: the prefixes of the signed-in interface
// and the two addresses that are open without a session.
type appRouten struct {
	Prefixes []string `json:"appPrefixes"`
	Offen    []string `json:"publicPaths"`
}

// ladeAppRouten reads dist/app-routes.json. If the file is missing (tests with
// a hand-built FS, a build that did not run vite) the list stays empty and
// every path counts as an app path — serving the shell is the safer wrong
// answer than a 404 on a page that does exist.
func ladeAppRouten(dist fs.FS) appRouten {
	var r appRouten
	if dist == nil {
		return r
	}
	data, err := fs.ReadFile(dist, "app-routes.json")
	if err != nil {
		return r
	}
	_ = json.Unmarshal(data, &r)
	return r
}

// bekannt reports whether the build handed over its route list.
func (a appRouten) bekannt() bool { return len(a.Prefixes) > 0 }

// istAppPfad recognises what the SPA shell has to answer: the root, the two
// open pages, and every path of the signed-in interface. Everything else is a
// typo and deserves an honest 404.
func (a appRouten) istAppPfad(path string) bool {
	if !a.bekannt() {
		return true
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path == "/" {
		return true
	}
	for _, p := range a.Offen {
		if path == p {
			return true
		}
	}
	for _, p := range a.Prefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// origin is the address under which the public website is reachable — the basis
// for canonical, hreflang and sitemap.
//
// Deliberately NOT PublicURL: that is the address over which sandboxes reach the
// control plane, and therefore an internal operational address (with the docker
// provider often a loopback that becomes host.docker.internal for the sandbox).
// Hanging both on one variable would mean nobody can set the website address
// without taking the data plane off the network.
//
// The default is therefore the request's own origin. The site.url setting
// and, behind it, COVEY_SITE_URL are the way out for setups in which the proxy
// does not pass it through — and the way to stop a stranger's Host header
// from deciding where a confirmation link points (#180).
func (s *Server) origin(r *http.Request) string {
	if s.Settings != nil {
		if v := s.Settings.SiteURLValue(r.Context(), s.SiteURL); v != "" {
			return v
		}
	} else if s.SiteURL != "" {
		return strings.TrimRight(s.SiteURL, "/")
	}
	schema := "http"
	if r.TLS != nil {
		schema = "https"
	}
	// Behind a reverse proxy TLS terminates in front of us. The header is not
	// trustworthy, but here it can only distort the address in our own response
	// — the one who takes it seriously is the crawler, not the server. Whoever
	// finds that too lax sets COVEY_PUBLIC_URL.
	if p := r.Header.Get("X-Forwarded-Proto"); p == "https" {
		schema = "https"
	}
	return schema + "://" + r.Host
}

// handleRobots — the directions for crawlers, and they are short.
//
// This binary serves an administration interface. Without a session it shows a
// sign-in form and nothing else; its address space is infinite (every agent ID
// its own URL), and the pages worth finding live on the website, under their
// own domain. So: nothing here belongs in an index.
//
// It stays a handler rather than a file in dist/ because an installation may
// well want to say something else one day, and because the file would then have
// to be maintained in the frontend build for a decision that belongs to the
// server.
func (s *Server) handleRobots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "User-agent: *\nDisallow: /\n")
}
