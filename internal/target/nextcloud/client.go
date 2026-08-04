// Package nextcloud binds Nextcloud file stores in as a target-system plugin:
// a public share link (or an account login) is addressed over WebDAV, and
// beneath it the agent can list, read and write files, fetch them into the
// sandbox and deposit them again. Unlike the SharePoint plugin it needs no
// OAuth flow — basic auth over WebDAV (public.php resp. remote.php) is enough;
// "sending a bot a link" suffices for access.
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

// uploadMaxBytes caps the size of a PUT upload. Overridable through
// COVEY_NEXTCLOUD_UPLOAD_MAX_MB (the daemon's process env).
func uploadMaxBytes() int64 {
	if mb, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_NEXTCLOUD_UPLOAD_MAX_MB"))); err == nil && mb > 0 {
		return int64(mb) << 20
	}
	return 250 << 20
}

// readMaxBytes caps how much text the read action returns straight into the
// session — larger files belong in the sandbox by way of download.
const readMaxBytes = 1 << 20

// listMax caps the number of entries list returns — WebDAV delivers a whole
// collection at once; very large folders would otherwise flood the session.
const listMax = 500

// Client speaks WebDAV with brokered basic-auth credentials. They come from
// the broker per call — they are never persisted.
type Client struct {
	DavBase string // WebDAV collection root, with a trailing "/"
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

// itemURL addresses an item relative to the WebDAV root. dir=true appends a
// trailing slash (the WebDAV convention for collections on PROPFIND/MKCOL).
// Path segments are escaped one by one.
func (c *Client) itemURL(relPath string, dir bool) string {
	u := c.DavBase // ends in "/"
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

// do runs a WebDAV request and checks the status against the codes accepted
// as "ok". Returns the body (the caller closes nothing — do reads it in full,
// except when streaming through Download).
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

// davError turns a WebDAV error response into a compact error. On failures
// Nextcloud often delivers a <d:error><s:message>… document.
func davError(method, u string, status int, data []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("webdav %s: HTTP 401 — wrong credentials (check the share password resp. user:app-password)", method)
	case http.StatusNotFound:
		return fmt.Errorf("webdav %s: HTTP 404 — path not found", method)
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

// redact strips the share token/user from a URL for error messages (basic auth
// lives in the header, and the public.php path is harmless — this is only
// about compact output).
func redact(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		if j := strings.IndexByte(u[i+3:], '/'); j >= 0 {
			return u[:i+3] + "…" + u[i+3+j:]
		}
	}
	return u
}

// --- PROPFIND parsing ------------------------------------------------------

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

// prop200 returns the properties from a response's 200 propstat (WebDAV puts
// properties it did not find into a separate 404 propstat).
func (r msResponse) prop200() (msProp, bool) {
	for _, ps := range r.Propstat {
		if strings.Contains(ps.Status, " 200") {
			return ps.Prop, true
		}
	}
	return msProp{}, false
}

// Entry is one entry of a folder listing resp. the metadata result of a write
// action.
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

// List — PROPFIND Depth:1. Returns the children of relPath, the display name
// of the root collection and whether listMax cut the listing short.
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
		return nil, "", false, fmt.Errorf("webdav response not readable: %w", err)
	}
	self := c.relFromHref(c.itemURL(relPath, true)) // = relPath (normalized)
	for _, r := range ms.Responses {
		rel := c.relFromHref(r.Href)
		prop, ok := r.prop200()
		if rel == self { // the requested collection itself — do not list it as a child
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

// relFromHref maps an href from the WebDAV response (possibly absolute,
// possibly percent-encoded) back onto a path relative to DavBase.
func (c *Client) relFromHref(href string) string {
	p := href
	if u, err := url.Parse(href); err == nil {
		p = u.Path // decodes the percent encoding
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

// Download — GET. Returns the open body; the caller closes it.
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

// Upload — PUT. Creates missing intermediate folders where needed (Nextcloud
// does not create them itself on a PUT) and retries the PUT once.
func (c *Client) Upload(ctx context.Context, relPath string, data []byte) (Entry, error) {
	if relPath == "" {
		return Entry{}, fmt.Errorf("target path missing")
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

// Delete — DELETE. The WebDAV root itself is off limits.
func (c *Client) Delete(ctx context.Context, relPath string) error {
	if relPath == "" {
		return fmt.Errorf("the root of the share cannot be deleted")
	}
	_, err := c.do(ctx, http.MethodDelete, c.itemURL(relPath, false), nil, nil,
		http.StatusOK, http.StatusNoContent)
	return err
}

// Mkdir creates a folder path (mkdir -p): every segment through MKCOL, folders
// that already exist (405 Method Not Allowed) are not an error.
func (c *Client) Mkdir(ctx context.Context, relPath string) (Entry, error) {
	if relPath == "" {
		return Entry{}, fmt.Errorf("path missing")
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

// cleanRemotePath normalizes a remote path supplied by the agent and rejects
// anything that would break out of the WebDAV root.
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
		return "", fmt.Errorf("path %q leaves the share (\"..\" is not allowed)", p)
	}
	return cleaned, nil
}
