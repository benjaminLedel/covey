package httpapi

// robots.txt and sitemap.xml. Both come out of the binary and not as fixed
// files from dist/, because their content contains the address of the
// installation — and only the running server knows it. Covey is self-hosted; a
// hard-wired domain would be wrong for every installation but one.
//
// The list of addresses itself comes from dist/seo.json, which the build writes
// while prerendering (web/prerender.mjs). That way there is one source for
// routing, prerendering and sitemap: web/src/public/seo.ts.

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// seoURL is an indexable address together with its language counterparts.
type seoURL struct {
	Path     string            `json:"path"`
	Lang     string            `json:"lang"`
	Priority float64           `json:"priority"`
	Alt      map[string]string `json:"alt"`
}

// seoIndex is dist/seo.json: what is public and what belongs to the signed-in
// interface.
type seoIndex struct {
	URLs        []seoURL `json:"urls"`
	AppPrefixes []string `json:"appPrefixes"`
}

// ladeSEOIndex reads dist/seo.json. If the file is missing (tests with a
// hand-built FS, a build without prerendering) the index stays empty: the
// sitemap is then empty and every unknown path counts as an app path — exactly
// the behaviour from before.
func ladeSEOIndex(dist fs.FS) seoIndex {
	var idx seoIndex
	if dist == nil {
		return idx
	}
	data, err := fs.ReadFile(dist, "seo.json")
	if err != nil {
		return idx
	}
	_ = json.Unmarshal(data, &idx)
	return idx
}

// vorgerendert reports whether the build prerendered the public pages. Only
// then may an unknown path become a 404 — otherwise the server would not know
// which public paths exist and would throw away real pages.
func (i seoIndex) vorgerendert() bool { return len(i.URLs) > 0 }

// istAppPfad recognises the paths of the signed-in interface. They fall back to
// the SPA shell; everything else gets a 404.
func (i seoIndex) istAppPfad(path string) bool {
	if !i.vorgerendert() {
		return true // without an index there is no distinction — better to serve
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for _, p := range i.AppPrefixes {
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
// The default is therefore the request's own origin; COVEY_SITE_URL is the way
// out for setups in which the proxy does not pass it through.
func (s *Server) origin(r *http.Request) string {
	if s.SiteURL != "" {
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

// handleRobots — the directions for crawlers.
func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	origin := s.origin(r)

	var b strings.Builder
	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /\n")
	// The API and the signed-in interface do not belong in the index: without a
	// session they deliver nothing readable anyway, and their address space is
	// infinite (every agent ID its own URL).
	b.WriteString("Disallow: /api/\n")
	for _, p := range s.seo.AppPrefixes {
		fmt.Fprintf(&b, "Disallow: %s\n", p)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Sitemap: %s/sitemap.xml\n", origin)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, b.String())
}

/* sitemap.xml. The language counterparts are included as xhtml:link — without
   them Google reads the German and the English version as two pages competing
   for the same query instead of as translations. */

type sitemapURLSet struct {
	XMLName xml.Name       `xml:"urlset"`
	NS      string         `xml:"xmlns,attr"`
	XHTML   string         `xml:"xmlns:xhtml,attr"`
	URLs    []sitemapEntry `xml:"url"`
}

type sitemapEntry struct {
	Loc      string          `xml:"loc"`
	Links    []sitemapAltLnk `xml:"xhtml:link"`
	Priority string          `xml:"priority"`
}

type sitemapAltLnk struct {
	Rel      string `xml:"rel,attr"`
	Hreflang string `xml:"hreflang,attr"`
	Href     string `xml:"href,attr"`
}

func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	origin := s.origin(r)

	set := sitemapURLSet{
		NS:    "http://www.sitemaps.org/schemas/sitemap/0.9",
		XHTML: "http://www.w3.org/1999/xhtml",
	}
	for _, u := range s.seo.URLs {
		e := sitemapEntry{
			Loc:      origin + u.Path,
			Priority: fmt.Sprintf("%.1f", u.Priority),
		}
		for _, lang := range []string{"de", "en"} {
			if href, ok := u.Alt[lang]; ok {
				e.Links = append(e.Links, sitemapAltLnk{"alternate", lang, origin + href})
			}
		}
		if href, ok := u.Alt["de"]; ok {
			e.Links = append(e.Links, sitemapAltLnk{"alternate", "x-default", origin + href})
		}
		set.URLs = append(set.URLs, e)
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(set)
}
