// Package nextcloud bindet Nextcloud-Dateiablagen als Zielsystem-Plugin an:
// ein öffentlicher Freigabelink (oder ein Konto-Zugang) wird über WebDAV
// angesprochen, darunter kann der Agent Dateien listen, lesen, schreiben, in
// die Sandbox laden und ablegen. Anders als das SharePoint-Plugin braucht es
// keinen OAuth-Flow — Basic-Auth über WebDAV (public.php bzw. remote.php)
// reicht; „einem Bot einen Link schicken" genügt für den Zugriff.
package nextcloud

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"covey/internal/reqlog"
)

// uploadMaxBytes begrenzt die Größe eines PUT-Uploads. Überschreibbar via
// COVEY_NEXTCLOUD_UPLOAD_MAX_MB (Prozess-Env des Daemons).
func uploadMaxBytes() int64 {
	if mb, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_NEXTCLOUD_UPLOAD_MAX_MB"))); err == nil && mb > 0 {
		return int64(mb) << 20
	}
	return 250 << 20
}

// readMaxBytes begrenzt, wie viel Text die read-Aktion direkt in die Session
// zurückgibt — größere Dateien gehören per download in die Sandbox.
const readMaxBytes = 1 << 20

// listMax begrenzt die Anzahl der Einträge, die list zurückgibt — WebDAV
// liefert eine ganze Collection auf einmal; sehr große Ordner würden sonst
// die Session fluten.
const listMax = 500

// Client spricht WebDAV mit gebrokerten Basic-Auth-Zugangsdaten. Die kommen
// pro Aufruf aus dem Broker — sie werden nie persistiert.
type Client struct {
	DavBase string // WebDAV-Collection-Wurzel, mit abschließendem "/"
	User    string
	Pass    string
	HTTP    *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{
		DavBase: cfg.DavBase,
		User:    cfg.User,
		Pass:    cfg.Pass,
		HTTP:    reqlog.Client("nextcloud", 60*time.Second),
	}
}

// itemURL adressiert ein Item relativ zur WebDAV-Wurzel. dir=true hängt einen
// abschließenden Schrägstrich an (WebDAV-Konvention für Collections bei
// PROPFIND/MKCOL). Pfadsegmente werden einzeln escaped.
func (c *Client) itemURL(relPath string, dir bool) string {
	u := c.DavBase // endet auf "/"
	if relPath != "" {
		u += escapePath(relPath)
		if dir {
			u += "/"
		}
	}
	return u
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// do führt eine WebDAV-Anfrage aus und prüft den Status gegen die als „ok"
// akzeptierten Codes. Liefert den Body zurück (der Aufrufer schließt nichts —
// do liest ihn vollständig, außer bei Streaming über Download).
func (c *Client) do(ctx context.Context, method, u string, body io.Reader, headers map[string]string, okCodes ...int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.User, c.Pass)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	for _, code := range okCodes {
		if resp.StatusCode == code {
			return data, nil
		}
	}
	return nil, davError(method, u, resp.StatusCode, data)
}

// davError macht aus einer WebDAV-Fehlerantwort einen kompakten Fehler.
// Nextcloud liefert bei Fehlern oft ein <d:error><s:message>…-Dokument.
func davError(method, u string, status int, data []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("webdav %s: HTTP 401 — Zugangsdaten falsch (Share-Passwort bzw. Benutzer:App-Passwort prüfen)", method)
	case http.StatusNotFound:
		return fmt.Errorf("webdav %s: HTTP 404 — Pfad nicht gefunden", method)
	}
	var e struct {
		Message   string `xml:"message"`
		Exception string `xml:"exception"`
	}
	if xml.Unmarshal(data, &e) == nil && strings.TrimSpace(e.Message) != "" {
		return fmt.Errorf("webdav %s: HTTP %d: %.200s", method, status, strings.TrimSpace(e.Message))
	}
	return fmt.Errorf("webdav %s %s: HTTP %d: %.200s", method, redact(u), status, data)
}

// redact entfernt den Share-Token/Benutzer aus einer URL für Fehlermeldungen
// (Basic-Auth steckt im Header, aber der public.php-Pfad ist harmlos — hier
// geht es nur um kompakte Ausgaben).
func redact(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		if j := strings.IndexByte(u[i+3:], '/'); j >= 0 {
			return u[:i+3] + "…" + u[i+3+j:]
		}
	}
	return u
}

// --- PROPFIND-Parsing ------------------------------------------------------

type multistatus struct {
	XMLName   xml.Name     `xml:"DAV: multistatus"`
	Responses []msResponse `xml:"DAV: response"`
}

type msResponse struct {
	Href     string       `xml:"DAV: href"`
	Propstat []msPropstat `xml:"DAV: propstat"`
}

type msPropstat struct {
	Status string `xml:"DAV: status"`
	Prop   msProp `xml:"DAV: prop"`
}

type msProp struct {
	DisplayName  string `xml:"DAV: displayname"`
	ContentLen   string `xml:"DAV: getcontentlength"`
	Modified     string `xml:"DAV: getlastmodified"`
	ResourceType struct {
		Collection *xml.Name `xml:"DAV: collection"`
	} `xml:"DAV: resourcetype"`
}

// prop200 liefert die Properties aus dem 200-propstat einer Response (WebDAV
// packt nicht gefundene Properties in einen separaten 404-propstat).
func (r msResponse) prop200() (msProp, bool) {
	for _, ps := range r.Propstat {
		if strings.Contains(ps.Status, " 200") {
			return ps.Prop, true
		}
	}
	return msProp{}, false
}

// Entry ist ein Eintrag einer Ordner-Auflistung bzw. das Metadaten-Ergebnis
// einer Schreib-Aktion.
type Entry struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Type     string `json:"type"` // "file" | "folder"
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:"><d:prop>
<d:displayname/><d:resourcetype/><d:getcontentlength/><d:getlastmodified/>
</d:prop></d:propfind>`

// List — PROPFIND Depth:1. Liefert die Kinder von relPath, den Anzeigenamen
// der Wurzel-Collection und ob wegen listMax abgeschnitten wurde.
func (c *Client) List(ctx context.Context, relPath string) (entries []Entry, rootName string, truncated bool, err error) {
	data, err := c.do(ctx, "PROPFIND", c.itemURL(relPath, true),
		strings.NewReader(propfindBody),
		map[string]string{"Depth": "1", "Content-Type": "application/xml; charset=utf-8"},
		http.StatusMultiStatus)
	if err != nil {
		return nil, "", false, err
	}
	var ms multistatus
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, "", false, fmt.Errorf("webdav-antwort nicht lesbar: %w", err)
	}
	self := c.relFromHref(c.itemURL(relPath, true)) // = relPath (normalisiert)
	for _, r := range ms.Responses {
		rel := c.relFromHref(r.Href)
		prop, ok := r.prop200()
		if rel == self { // die angefragte Collection selbst — nicht als Kind listen
			if ok {
				rootName = prop.DisplayName
			}
			continue
		}
		if !ok {
			continue
		}
		if len(entries) >= listMax {
			truncated = true
			break
		}
		entries = append(entries, toEntry(rel, prop))
	}
	return entries, rootName, truncated, nil
}

// relFromHref rechnet einen (evtl. absoluten, evtl. prozent-kodierten) href
// aus der WebDAV-Antwort auf einen Pfad relativ zur DavBase zurück.
func (c *Client) relFromHref(href string) string {
	p := href
	if u, err := url.Parse(href); err == nil {
		p = u.Path // dekodiert Prozent-Kodierung
	}
	base := c.DavBase
	if u, err := url.Parse(c.DavBase); err == nil {
		base = u.Path
	}
	p = strings.TrimPrefix(strings.Trim(p, "/"), strings.Trim(base, "/"))
	return strings.Trim(p, "/")
}

func toEntry(rel string, p msProp) Entry {
	e := Entry{Name: path.Base(rel), Path: rel, Type: "file", Modified: p.Modified}
	if p.DisplayName != "" {
		e.Name = p.DisplayName
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(p.ContentLen), 10, 64); err == nil {
		e.Size = n
	}
	if p.ResourceType.Collection != nil {
		e.Type = "folder"
	}
	return e
}

// Download — GET. Gibt den offenen Body zurück; der Aufrufer schließt ihn.
func (c *Client) Download(ctx context.Context, relPath string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.itemURL(relPath, false), nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.User, c.Pass)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, davError(http.MethodGet, relPath, resp.StatusCode, data)
	}
	return resp.Body, nil
}

// Upload — PUT. Legt fehlende Zwischenordner bei Bedarf an (Nextcloud legt
// sie beim PUT nicht selbst an) und wiederholt den PUT einmal.
func (c *Client) Upload(ctx context.Context, relPath string, data []byte) (Entry, error) {
	if relPath == "" {
		return Entry{}, fmt.Errorf("zielpfad fehlt")
	}
	put := func() error {
		_, err := c.do(ctx, http.MethodPut, c.itemURL(relPath, false),
			bytes.NewReader(data), map[string]string{"Content-Type": "application/octet-stream"},
			http.StatusOK, http.StatusCreated, http.StatusNoContent)
		return err
	}
	err := put()
	if err != nil && strings.Contains(err.Error(), "HTTP 409") {
		if parent := path.Dir(relPath); parent != "." && parent != "/" {
			if _, mkErr := c.Mkdir(ctx, parent); mkErr == nil {
				err = put()
			}
		}
	}
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: path.Base(relPath), Path: relPath, Type: "file", Size: int64(len(data))}, nil
}

// Delete — DELETE. Die WebDAV-Wurzel selbst ist tabu.
func (c *Client) Delete(ctx context.Context, relPath string) error {
	if relPath == "" {
		return fmt.Errorf("die Wurzel der Freigabe kann nicht gelöscht werden")
	}
	_, err := c.do(ctx, http.MethodDelete, c.itemURL(relPath, false), nil, nil,
		http.StatusOK, http.StatusNoContent)
	return err
}

// Mkdir legt einen Ordnerpfad an (mkdir -p): jedes Segment per MKCOL, bereits
// vorhandene Ordner (405 Method Not Allowed) sind kein Fehler.
func (c *Client) Mkdir(ctx context.Context, relPath string) (Entry, error) {
	if relPath == "" {
		return Entry{}, fmt.Errorf("pfad fehlt")
	}
	segs := strings.Split(relPath, "/")
	parent := ""
	for _, seg := range segs {
		parent = path.Join(parent, seg)
		_, err := c.do(ctx, "MKCOL", c.itemURL(parent, true), nil, nil,
			http.StatusCreated, http.StatusOK, http.StatusMethodNotAllowed)
		if err != nil {
			return Entry{}, err
		}
	}
	return Entry{Name: path.Base(relPath), Path: relPath, Type: "folder"}, nil
}

// cleanRemotePath normalisiert einen vom Agenten gelieferten Remote-Pfad und
// weist alles ab, was aus der WebDAV-Wurzel ausbrechen würde.
func cleanRemotePath(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return "", nil
	}
	cleaned := path.Clean(p)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("pfad %q verlässt die Freigabe (\"..\" ist nicht erlaubt)", p)
	}
	return cleaned, nil
}
