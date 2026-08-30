package httpapi

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
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

// spaHandler serves the interface. Three cases, in this order:
//
//  1. a static file (assets, images, manifest),
//  2. a path the SPA knows → the shell,
//  3. otherwise: an honest 404.
//
// Until #130 there was a fourth case in front: a prerendered public page. The
// website lived in the binary and brought its own HTML per address; that is
// gone, and with it the placeholder for the installation's own origin, which
// only the prerendered head needed.
//
// Case 3 is worth keeping even though nothing is indexed here any more: a typo
// should say so instead of quietly showing the sign-in. Without a route list
// (an old build, tests with a hand-built FS) everything falls to the shell —
// see appRouten.istAppPfad.
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
				setzeCacheHeader(w, path)
				if serviereVorkomprimiert(w, r, dist, path) {
					return
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		if s.routen.istAppPfad(r.URL.Path) {
			// The interface itself does not belong in the index: without a
			// session it shows nothing, and its address space is infinite.
			w.Header().Set("X-Robots-Tag", "noindex")
			daten, err := fs.ReadFile(dist, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			s.schreibeHTML(w, r, daten, http.StatusOK)
			return
		}

		/* Bis #130 stand hier eine vorgerenderte 404-Seite je Sprache. Ohne
		   Website gibt es sie nicht mehr, und für eine Anwendung, die nichts
		   indexieren lässt, reicht die schlichte Antwort des Servers. */
		http.NotFound(w, r)
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

// schreibeHTML serves the SPA shell.
func (s *Server) schreibeHTML(w http.ResponseWriter, _ *http.Request, daten []byte, status int) {
	html := string(daten)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The HTML is assembled per request — the address of this installation goes
	// in above — and it names the hashed assets of the current build. A cache
	// that keeps it hands out yesterday's asset names after a deploy. no-cache
	// does not forbid storing it, it requires asking first; the ETag then still
	// answers most of those questions with a 304.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", strconv.Itoa(len(html)))
	w.WriteHeader(status)
	_, _ = io.WriteString(w, html)
}

// setzeCacheHeader decides how long a static file may be held. Without this
// header a browser gets nothing to go on and asks again on every visit — the
// second visit then pays a round trip per file for a 304 that says "unchanged".
//
// The distinction is not the file type but whether the name changes with the
// content. Vite writes everything under assets/ as name-<hash>.ext: a changed
// file gets a new name, so the old one can never be wrong and may be kept for
// as long as a browser is willing to (a year is the accepted maximum).
// istGehasht checks that rather than trusting the directory — a file that lands
// there without a hash would otherwise be frozen in every visitor's cache.
//
// Everything beside it (the images under shots/ and landing/, the favicons, the
// manifest) keeps its name across releases. A day, and a revalidation after
// that: long enough to carry a session, short enough that a replaced screenshot
// arrives the next day rather than next year.
func setzeCacheHeader(w http.ResponseWriter, pfad string) {
	if strings.HasPrefix(pfad, "assets/") && istGehasht(pfad) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
}

// istGehasht: does this name carry a content hash before its extension? Vite
// builds them from base64url, eight characters — index-DOnPDnv_.js,
// inter-latin-400-normal-C38fXH4l.woff2.
var gehashtesAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)

func istGehasht(pfad string) bool { return gehashtesAsset.MatchString(pfad) }

// vorkomprimierteFormen: the encodings the build writes beside a file
// (web/compress.mjs), in the order of preference. Brotli first — it is what
// saves the most and what every browser that can read this bundle understands.
var vorkomprimierteFormen = []struct{ encoding, endung string }{
	{"br", ".br"},
	{"gzip", ".gz"},
}

// serviereVorkomprimiert hands out the compressed form of a static file when
// the client accepts that encoding and the build produced one. It says whether
// it answered.
//
// The alternative is what happens without it: mitKompression compresses the
// same bundle again for every visitor, at gzip's fastest setting, because that
// is the right trade for an API answer computed per request. A file that is
// built once and identical until the next release deserves the opposite trade —
// compressed once, as well as the compressor can.
//
// Content-Encoding is set before anything is written, which is also what keeps
// mitKompression out of the way: it leaves an answer alone that is already
// encoded.
func serviereVorkomprimiert(w http.ResponseWriter, r *http.Request, dist fs.FS, pfad string) bool {
	for _, form := range vorkomprimierteFormen {
		if !akzeptiertEncoding(r, form.encoding) {
			continue
		}
		daten, err := fs.ReadFile(dist, pfad+form.endung)
		if err != nil {
			continue
		}
		h := w.Header()
		// The type is the one of the original file — the encoding is what the
		// compression says, not the content type. Without this the browser
		// would get application/octet-stream and download the stylesheet
		// instead of applying it.
		if typ := mime.TypeByExtension(strings.ToLower(path.Ext(pfad))); typ != "" {
			h.Set("Content-Type", typ)
		}
		h.Set("Content-Encoding", form.encoding)
		// The same address answers differently depending on the request header.
		// A cache that does not know that hands a brotli body to a client that
		// cannot read it.
		h.Set("Vary", "Accept-Encoding")
		h.Set("Content-Length", strconv.Itoa(len(daten)))
		_, _ = w.Write(daten)
		return true
	}
	return false
}

// akzeptiertEncoding: did the client name this encoding in Accept-Encoding? A
// q=0 is a refusal — "gzip;q=0" means explicitly not that one.
func akzeptiertEncoding(r *http.Request, encoding string) bool {
	for _, teil := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, rest, _ := strings.Cut(strings.TrimSpace(teil), ";")
		if !strings.EqualFold(strings.TrimSpace(name), encoding) {
			continue
		}
		return strings.ReplaceAll(rest, " ", "") != "q=0"
	}
	return false
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
