// Package sharepoint binds SharePoint/Teams document libraries in as a
// target-system plugin: a share link is resolved to the library through the
// Microsoft Graph API (the /shares endpoint), and beneath it the agent can
// list, read and write files, fetch them into the sandbox and deposit them.
package sharepoint

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"covey/internal/reqlog"
)

// uploadMaxBytes caps the size of a simple upload (PUT …/content). Graph
// allows up to 250 MB for that; larger files would need an upload session
// (deliberately not part of the MVP). Overridable through
// COVEY_SHAREPOINT_UPLOAD_MAX_MB (the daemon's process env).
func uploadMaxBytes() int64 {
	if mb, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_SHAREPOINT_UPLOAD_MAX_MB"))); err == nil && mb > 0 {
		return int64(mb) << 20
	}
	return 250 << 20
}

// readMaxBytes caps how much text the read action returns straight into the
// session — larger files belong in the sandbox by way of download.
const readMaxBytes = 1 << 20

// Client speaks the Microsoft Graph API with a bearer token (brokered resp.
// fetched through the client-credentials flow). The token comes from the
// broker/cache per call — it is never persisted.
type Client struct {
	Graph string // base without /v1.0
	Token string
	HTTP  *http.Client
}

func NewClient(graphBase, token string) *Client {
	return &Client{
		Graph: strings.TrimRight(graphBase, "/"),
		Token: token,
		HTTP:  reqlog.Client("sharepoint", 60*time.Second),
	}
}

func (c *Client) do(ctx context.Context, method, u string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return graphError(method, u, resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// graphError turns a Graph error response into a compact error carrying the
// Graph error code (e.g. itemNotFound, nameAlreadyExists) — for the agent the
// code carries more information than the HTTP status.
func graphError(method, u string, status int, data []byte) error {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error.Code != "" {
		return fmt.Errorf("graph %s: HTTP %d %s: %.200s", method, status, e.Error.Code, e.Error.Message)
	}
	return fmt.Errorf("graph %s %s: HTTP %d: %.200s", method, u, status, data)
}

// isGraphCode checks whether an error carries the named Graph error code.
func isGraphCode(err error, code string) bool {
	return err != nil && strings.Contains(err.Error(), " "+code+":")
}

// Root is the resolved root of the share link — the document library resp.
// the folder beneath which all actions work.
type Root struct {
	DriveID string
	ItemID  string
	Name    string
	WebURL  string
}

// Resolving a share link is stable (the link always points at the same
// folder) — a short cache saves the extra roundtrip per action.
var (
	rootMu    sync.Mutex
	rootCache = map[string]cachedRoot{}
)

type cachedRoot struct {
	root    Root
	expires time.Time
}

// ResolveRoot resolves the share link to the drive root through
// GET /shares/{u!…}/driveItem. The link must point at a folder or a library —
// actions address files relative to it.
func (c *Client) ResolveRoot(ctx context.Context, shareLink string) (Root, error) {
	key := c.Graph + "|" + shareLink
	rootMu.Lock()
	cached, ok := rootCache[key]
	rootMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.root, nil
	}

	// Encoding per the Graph docs: "u!" + base64url(link) without padding.
	enc := "u!" + strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(shareLink)), "=")
	var item struct {
		ID     string          `json:"id"`
		Name   string          `json:"name"`
		WebURL string          `json:"webUrl"`
		Folder json.RawMessage `json:"folder"`
		Parent struct {
			DriveID string `json:"driveId"`
		} `json:"parentReference"`
	}
	u := c.Graph + "/v1.0/shares/" + enc + "/driveItem?$select=id,name,webUrl,folder,parentReference"
	if err := c.do(ctx, http.MethodGet, u, nil, "", &item); err != nil {
		return Root{}, fmt.Errorf("resolve share link: %w", err)
	}
	if item.Folder == nil {
		return Root{}, fmt.Errorf("the stored share link points at a file (%q) — store a folder or library link in sharepoint_url", item.Name)
	}
	root := Root{DriveID: item.Parent.DriveID, ItemID: item.ID, Name: item.Name, WebURL: item.WebURL}
	if root.DriveID == "" || root.ItemID == "" {
		return Root{}, fmt.Errorf("resolve share link: response without driveId/id")
	}
	rootMu.Lock()
	rootCache[key] = cachedRoot{root: root, expires: time.Now().Add(10 * time.Minute)}
	rootMu.Unlock()
	return root, nil
}

// itemURL addresses an item relative to the root: an empty path = the root
// itself, otherwise the Graph path syntax /items/{root}:/{path}:{suffix}.
// Path segments are escaped one by one (spaces, umlauts, …).
func (c *Client) itemURL(root Root, relPath, suffix string) string {
	base := c.Graph + "/v1.0/drives/" + url.PathEscape(root.DriveID) + "/items/" + url.PathEscape(root.ItemID)
	if relPath == "" {
		return base + suffix
	}
	segs := strings.Split(relPath, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	if suffix == "" {
		return base + ":/" + strings.Join(segs, "/")
	}
	return base + ":/" + strings.Join(segs, "/") + ":" + suffix
}

// cleanRemotePath normalizes a remote path supplied by the agent and rejects
// anything that would break out of the share root.
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

// Entry is one entry of a folder listing resp. the metadata result of a write
// action.
type Entry struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Type     string `json:"type"` // "file" | "folder"
	Size     int64  `json:"size"`
	Children int    `json:"children,omitempty"`
	Modified string `json:"modified,omitempty"`
	WebURL   string `json:"web_url,omitempty"`
}

// driveItem is the Graph representation Entry is built from.
type driveItem struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	WebURL   string `json:"webUrl"`
	Modified string `json:"lastModifiedDateTime"`
	Folder   *struct {
		ChildCount int `json:"childCount"`
	} `json:"folder"`
	File *struct{} `json:"file"`
}

func toEntry(it driveItem, relPath string) Entry {
	e := Entry{Name: it.Name, Path: relPath, Type: "file", Size: it.Size,
		Modified: it.Modified, WebURL: it.WebURL}
	if it.Folder != nil {
		e.Type = "folder"
		e.Children = it.Folder.ChildCount
	}
	return e
}

const listSelect = "?$select=name,size,lastModifiedDateTime,folder,file,webUrl&$top=200"

// List — GET …/children. One page (200 entries); truncated reports when Graph
// would have further pages.
func (c *Client) List(ctx context.Context, root Root, relPath string) ([]Entry, bool, error) {
	var out struct {
		Value    []driveItem `json:"value"`
		NextLink string      `json:"@odata.nextLink"`
	}
	if err := c.do(ctx, http.MethodGet, c.itemURL(root, relPath, "/children"+listSelect), nil, "", &out); err != nil {
		return nil, false, err
	}
	entries := make([]Entry, 0, len(out.Value))
	for _, it := range out.Value {
		entries = append(entries, toEntry(it, path.Join(relPath, it.Name)))
	}
	return entries, out.NextLink != "", nil
}

// Download — GET …/content. Graph answers with a redirect onto a
// pre-authorized download URL; the http.Client follows it (and correctly drops
// the Authorization header when the host changes).
func (c *Client) Download(ctx context.Context, root Root, relPath string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.itemURL(root, relPath, "/content"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		return nil, graphError(http.MethodGet, relPath, resp.StatusCode, data)
	}
	return resp.Body, nil
}

// Upload — PUT …/content (simple upload, replaces what is there).
func (c *Client) Upload(ctx context.Context, root Root, relPath string, r io.Reader) (Entry, error) {
	if relPath == "" {
		return Entry{}, fmt.Errorf("target path missing")
	}
	var it driveItem
	u := c.itemURL(root, relPath, "/content") + "?@microsoft.graph.conflictBehavior=replace"
	if err := c.do(ctx, http.MethodPut, u, r, "application/octet-stream", &it); err != nil {
		return Entry{}, err
	}
	return toEntry(it, relPath), nil
}

// Delete — DELETE on the item. The share root itself is off limits.
func (c *Client) Delete(ctx context.Context, root Root, relPath string) error {
	if relPath == "" {
		return fmt.Errorf("the root folder of the share cannot be deleted")
	}
	return c.do(ctx, http.MethodDelete, c.itemURL(root, relPath, ""), nil, "", nil)
}

// Mkdir creates a folder path (mkdir -p): every segment on its own, folders
// that already exist are not an error.
func (c *Client) Mkdir(ctx context.Context, root Root, relPath string) (Entry, error) {
	if relPath == "" {
		return Entry{}, fmt.Errorf("path missing")
	}
	segs := strings.Split(relPath, "/")
	parent := ""
	var last Entry
	for _, seg := range segs {
		body, _ := json.Marshal(map[string]any{
			"name": seg, "folder": map[string]any{},
			"@microsoft.graph.conflictBehavior": "fail",
		})
		var it driveItem
		err := c.do(ctx, http.MethodPost, c.itemURL(root, parent, "/children"),
			bytes.NewReader(body), "application/json", &it)
		parent = path.Join(parent, seg)
		switch {
		case err == nil:
			last = toEntry(it, parent)
		case isGraphCode(err, "nameAlreadyExists"):
			last = Entry{Name: seg, Path: parent, Type: "folder"}
		default:
			return Entry{}, err
		}
	}
	return last, nil
}
