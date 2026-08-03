package httpapi

// robots.txt und sitemap.xml. Beide kommen aus dem Binary und nicht als feste
// Dateien aus dist/, weil ihr Inhalt die Adresse der Installation enthält —
// und die kennt erst der laufende Server. Covey wird selbst gehostet; eine
// einbetonierte Domain wäre für jede Installation außer einer falsch.
//
// Die Liste der Adressen selbst kommt aus dist/seo.json, das der Build beim
// Vorrendern schreibt (web/prerender.mjs). Damit gibt es eine Quelle für
// Routing, Vorrendern und Sitemap: web/src/public/seo.ts.

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// seoURL ist eine indexierbare Adresse mit ihren Sprachgegenstücken.
type seoURL struct {
	Path     string            `json:"path"`
	Lang     string            `json:"lang"`
	Priority float64           `json:"priority"`
	Alt      map[string]string `json:"alt"`
}

// seoIndex ist dist/seo.json: was öffentlich ist und was zur angemeldeten
// Oberfläche gehört.
type seoIndex struct {
	URLs        []seoURL `json:"urls"`
	AppPrefixes []string `json:"appPrefixes"`
}

// ladeSEOIndex liest dist/seo.json. Fehlt die Datei (Tests mit einem
// handgebauten FS, ein Build ohne Vorrendern), bleibt der Index leer: Die
// Sitemap ist dann leer und jeder unbekannte Pfad gilt als App-Pfad — also
// genau das Verhalten von vorher.
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

// vorgerendert meldet, ob der Build die öffentlichen Seiten vorgerendert hat.
// Nur dann darf ein unbekannter Pfad eine 404 werden — sonst wüsste der Server
// nicht, welche Pfade es öffentlich gibt, und würde echte Seiten wegwerfen.
func (i seoIndex) vorgerendert() bool { return len(i.URLs) > 0 }

// istAppPfad erkennt die Pfade der angemeldeten Oberfläche. Sie fallen auf die
// SPA-Hülle zurück; alles andere bekommt eine 404.
func (i seoIndex) istAppPfad(pfad string) bool {
	if !i.vorgerendert() {
		return true // ohne Index keine Unterscheidung — lieber ausliefern
	}
	if !strings.HasPrefix(pfad, "/") {
		pfad = "/" + pfad
	}
	for _, p := range i.AppPrefixes {
		if pfad == p || strings.HasPrefix(pfad, p+"/") {
			return true
		}
	}
	return false
}

// origin ist die Adresse, unter der die öffentliche Website erreichbar ist —
// Grundlage für Canonical, hreflang und Sitemap.
//
// Bewusst NICHT PublicURL: Die ist die Adresse, über die Sandboxen die Control
// Plane erreichen, und damit eine interne Betriebsadresse (beim docker-Provider
// oft ein Loopback, das für die Sandbox zu host.docker.internal wird). Beides
// an eine Variable zu hängen hieße, dass niemand die Website-Adresse setzen
// kann, ohne die Data Plane vom Netz zu nehmen.
//
// Default ist deshalb die Herkunft des Requests; COVEY_SITE_URL ist der
// Ausweg für Aufbauten, in denen der Proxy sie nicht durchreicht.
func (s *Server) origin(r *http.Request) string {
	if s.SiteURL != "" {
		return strings.TrimRight(s.SiteURL, "/")
	}
	schema := "http"
	if r.TLS != nil {
		schema = "https"
	}
	// Hinter einem Reverse Proxy endet TLS davor. Der Header ist nicht
	// vertrauenswürdig, kann hier aber nur die Adresse in der eigenen Antwort
	// verfälschen — wer sie ernst nimmt, ist der Crawler, nicht der Server.
	// Wem das zu locker ist, setzt COVEY_PUBLIC_URL.
	if p := r.Header.Get("X-Forwarded-Proto"); p == "https" {
		schema = "https"
	}
	return schema + "://" + r.Host
}

// handleRobots — die Wegbeschreibung für Crawler.
func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	origin := s.origin(r)

	var b strings.Builder
	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /\n")
	// Die API und die angemeldete Oberfläche gehören nicht in den Index: Ohne
	// Sitzung liefern sie ohnehin nichts Lesbares, und ihr Adressraum ist
	// unendlich (jede Agenten-ID eine eigene URL).
	b.WriteString("Disallow: /api/\n")
	for _, p := range s.seo.AppPrefixes {
		fmt.Fprintf(&b, "Disallow: %s\n", p)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Sitemap: %s/sitemap.xml\n", origin)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, b.String())
}

/* sitemap.xml. Die Sprachgegenstücke stehen als xhtml:link mit dabei — ohne
   sie wertet Google die deutsche und die englische Fassung als zwei Seiten,
   die um dieselbe Suchanfrage konkurrieren, statt als Übersetzungen. */

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
