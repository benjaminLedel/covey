package httpapi

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
)

func init() {
	// Go does not know .webmanifest out of the box — otherwise the FileServer
	// would serve text/plain.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// platzhalterOrigin stands in the prerendered HTML everywhere the address of
// this installation belongs (canonical, hreflang, og:url, JSON-LD). The build
// does not know it — Covey is self-hosted. See seo.go.
const platzhalterOrigin = "__COVEY_ORIGIN__"

// spaHandler serves the website. Four cases, in this order:
//
//  1. a prerendered public page (dist/funktion/index.html …),
//  2. a static file (assets, images, manifest),
//  3. a path of the signed-in interface → the SPA shell, not indexable,
//  4. otherwise: an honest 404.
//
// Point 4 is the difference to before. Until then every path got index.html with
// status 200 — inconspicuous for visitors, a "soft 404" for search engines:
// arbitrarily many addresses that all show the same thing and all count as a
// valid page. Without a prerendered dist/ (old build, tests with a hand-built
// FS) the old behaviour remains — see seoIndex.istAppPfad.
func (s *Server) spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /funktion/ and /funktion would be two addresses with the same content.
		//
		// The target is built from the request path, so it has to stay a path.
		// `//evil.com/` would become `//evil.com` — for a browser that is not a
		// path but a protocol-relative URL, and thus an open redirect off our
		// own host. In production http.ServeMux normalises that away before we
		// see it (a 307 to /evil.com/), but that is a property of the mounting,
		// not of this handler: whoever mounts it differently would inherit the
		// hole. So the check belongs here, where the target is built.
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			ziel := strings.TrimRight(r.URL.Path, "/")
			if !istLokalerPfad(ziel) {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, ziel, http.StatusMovedPermanently)
			return
		}

		path := strings.Trim(r.URL.Path, "/")

		seite := "index.html"
		if path != "" {
			seite = path + "/index.html"
		}
		if daten, err := fs.ReadFile(dist, seite); err == nil {
			s.schreibeHTML(w, r, daten, http.StatusOK)
			return
		}

		if path != "" {
			if info, err := fs.Stat(dist, path); err == nil && !info.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		if s.seo.istAppPfad(r.URL.Path) {
			// The interface itself does not belong in the index: without a
			// session it shows nothing, and its address space is infinite.
			w.Header().Set("X-Robots-Tag", "noindex")
			daten, err := fs.ReadFile(dist, "app.html")
			if err != nil {
				// Build without prerendering — then index.html is the shell.
				daten, err = fs.ReadFile(dist, "index.html")
			}
			if err != nil {
				http.NotFound(w, r)
				return
			}
			s.schreibeHTML(w, r, daten, http.StatusOK)
			return
		}

		file := "404.html"
		if strings.HasPrefix(path, "en/") || path == "en" {
			file = "en/404.html"
		}
		daten, err := fs.ReadFile(dist, file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		s.schreibeHTML(w, r, daten, http.StatusNotFound)
	})
}

// istLokalerPfad says whether a redirect target stays on this host: exactly one
// leading slash. A browser reads `//host` as an address somewhere else, and
// `/\host` the same way — some proxies turn the backslash into a slash before
// the browser ever sees it.
//
// A scheme needs no check of its own: anything starting with a slash can no
// longer be one.
func istLokalerPfad(ziel string) bool {
	return strings.HasPrefix(ziel, "/") &&
		!strings.HasPrefix(ziel, "//") &&
		!strings.HasPrefix(ziel, "/\\")
}

// schreibeHTML inserts the address of this installation into the prerendered
// HTML and serves it.
func (s *Server) schreibeHTML(w http.ResponseWriter, r *http.Request, daten []byte, status int) {
	html := strings.ReplaceAll(string(daten), platzhalterOrigin, s.origin(r))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(html)))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}

// setzeSchutzHeader gives the admin interface the headers a browser needs in
// order to defend it. Until now they were missing entirely.
//
// Why this is more than a formality: the interface shows content that agents
// bring in from foreign sources — ticket texts, mails, wiki pages, output from
// target systems. Should any of it ever break through as markup, the CSP is the
// second line: without it an injected script could load more code and send data
// somewhere; with it, it cannot.
func setzeSchutzHeader(w http.ResponseWriter) {
	h := w.Header()
	// script-src 'self': Vite builds a single module bundle, no inline scripts
	// — so this suffices without 'unsafe-inline'.
	// style-src needs 'unsafe-inline' because the interface works with style
	// attributes throughout (React style={{…}}).
	// connect-src 'self': the SPA only talks to its own API.
	// frame-ancestors 'none' + X-Frame-Options: no embedding, therefore no
	// clickjacking on buttons like "Stop" or "Approve".
	// img-src data: for the embedded icons, blob: for preview images.
	h.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"object-src 'none'",
	}, "; "))
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	// No referrer to third parties — the URL carries agent and task IDs.
	h.Set("Referrer-Policy", "no-referrer")
	// The interface needs none of these; switch them off explicitly so that an
	// injected script does not get them either.
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
}

// findWebhookAgent resolves the address part of a webhook URL. Meant is the
// slug; the agent id is accepted as a substitute because it appears in the
// agent's UI URL and is easily copied instead of the slug when setting up a
// target system.
//
// The id is the address that always works: it is unique instance-wide,
// whereas a slug is unique only within its organisation. Where a slug is
// ambiguous, FindBySlug refuses and the caller gets a 404 that says so — the
// id then remains the way through (FR-003, finding B).
func (s *Server) findWebhookAgent(r *http.Request, ref string) (agents.Agent, error) {
	if a, err := s.Registry.FindBySlug(r.Context(), ref); err == nil {
		return a, nil
	}
	id, err := uuid.Parse(ref)
	if err != nil {
		return agents.Agent{}, agents.ErrNotFound
	}
	return s.Registry.Get(r.Context(), id)
}
