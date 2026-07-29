// Package sharepoint bindet SharePoint-/Teams-Dokumentbibliotheken als
// Zielsystem-Plugin an: ein Freigabelink wird über die Microsoft-Graph-API
// (/shares-Endpoint) zur Bibliothek aufgelöst, darunter kann der Agent
// Dateien listen, lesen, schreiben, in die Sandbox laden und ablegen.
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

// uploadMaxBytes begrenzt die Größe eines Simple-Uploads (PUT …/content).
// Graph erlaubt dafür bis 250 MB; größere Dateien bräuchten eine
// Upload-Session (bewusst nicht im MVP). Überschreibbar via
// COVEY_SHAREPOINT_UPLOAD_MAX_MB (Prozess-Env des Daemons).
func uploadMaxBytes() int64 {
	if mb, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_SHAREPOINT_UPLOAD_MAX_MB"))); err == nil && mb > 0 {
		return int64(mb) << 20
	}
	return 250 << 20
}

// readMaxBytes begrenzt, wie viel Text die read-Aktion direkt in die
// Session zurückgibt — größere Dateien gehören per download in die Sandbox.
const readMaxBytes = 1 << 20

// Client spricht die Microsoft-Graph-API mit einem (gebrokerten bzw. über
// den Client-Credentials-Flow geholten) Bearer-Token. Das Token kommt pro
// Aufruf aus dem Broker/Cache — es wird nie persistiert.
type Client struct {
	Graph string // Basis ohne /v1.0
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

// graphError macht aus einer Graph-Fehlerantwort einen kompakten Fehler mit
// dem Graph-Fehlercode (z. B. itemNotFound, nameAlreadyExists) — der Code
// trägt für den Agenten mehr Information als der HTTP-Status.
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

// isGraphCode prüft, ob ein Fehler den genannten Graph-Fehlercode trägt.
func isGraphCode(err error, code string) bool {
	return err != nil && strings.Contains(err.Error(), " "+code+":")
}

// Root ist die aufgelöste Wurzel des Freigabelinks — die Dokumentbibliothek
// bzw. der Ordner, unter dem alle Aktionen arbeiten.
type Root struct {
	DriveID string
	ItemID  string
	Name    string
	WebURL  string
}

// Freigabelink-Auflösung ist stabil (der Link zeigt immer auf denselben
// Ordner) — ein kurzer Cache spart den zusätzlichen Roundtrip pro Aktion.
var (
	rootMu    sync.Mutex
	rootCache = map[string]cachedRoot{}
)

type cachedRoot struct {
	root    Root
	expires time.Time
}

// ResolveRoot löst den Freigabelink über GET /shares/{u!…}/driveItem zur
// Drive-Wurzel auf. Der Link muss auf einen Ordner oder eine Bibliothek
// zeigen — Aktionen adressieren Dateien relativ dazu.
func (c *Client) ResolveRoot(ctx context.Context, shareLink string) (Root, error) {
	key := c.Graph + "|" + shareLink
	rootMu.Lock()
	cached, ok := rootCache[key]
	rootMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.root, nil
	}

	// Encoding laut Graph-Doku: "u!" + Base64url(Link) ohne Padding.
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
		return Root{}, fmt.Errorf("freigabelink aufloesen: %w", err)
	}
	if item.Folder == nil {
		return Root{}, fmt.Errorf("der hinterlegte Freigabelink zeigt auf eine Datei (%q) — einen Ordner- oder Bibliotheks-Link in sharepoint_url hinterlegen", item.Name)
	}
	root := Root{DriveID: item.Parent.DriveID, ItemID: item.ID, Name: item.Name, WebURL: item.WebURL}
	if root.DriveID == "" || root.ItemID == "" {
		return Root{}, fmt.Errorf("freigabelink aufloesen: Antwort ohne driveId/id")
	}
	rootMu.Lock()
	rootCache[key] = cachedRoot{root: root, expires: time.Now().Add(10 * time.Minute)}
	rootMu.Unlock()
	return root, nil
}

// itemURL adressiert ein Item relativ zur Root: leerer Pfad = die Root
// selbst, sonst die Graph-Pfadsyntax /items/{root}:/{pfad}:{suffix}.
// Pfadsegmente werden einzeln escaped (Leerzeichen, Umlaute, …).
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

// cleanRemotePath normalisiert einen vom Agenten gelieferten Remote-Pfad
// und weist alles ab, was aus der Freigabe-Wurzel ausbrechen würde.
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
		return "", fmt.Errorf("pfad %q verlaesst die Freigabe (\"..\" ist nicht erlaubt)", p)
	}
	return cleaned, nil
}

// Entry ist ein Eintrag einer Ordner-Auflistung bzw. das Metadaten-Ergebnis
// einer Schreib-Aktion.
type Entry struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Type     string `json:"type"` // "file" | "folder"
	Size     int64  `json:"size"`
	Children int    `json:"children,omitempty"`
	Modified string `json:"modified,omitempty"`
	WebURL   string `json:"web_url,omitempty"`
}

// driveItem ist die Graph-Repräsentation, aus der Entry gebaut wird.
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

// List — GET …/children. Eine Seite (200 Einträge); truncated meldet, wenn
// Graph weitere Seiten hätte.
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

// Download — GET …/content. Graph antwortet mit einem Redirect auf eine
// vorautorisierte Download-URL; der http.Client folgt ihm (und lässt den
// Authorization-Header beim Host-Wechsel korrekt weg).
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

// Upload — PUT …/content (Simple Upload, ersetzt Vorhandenes).
func (c *Client) Upload(ctx context.Context, root Root, relPath string, r io.Reader) (Entry, error) {
	if relPath == "" {
		return Entry{}, fmt.Errorf("zielpfad fehlt")
	}
	var it driveItem
	u := c.itemURL(root, relPath, "/content") + "?@microsoft.graph.conflictBehavior=replace"
	if err := c.do(ctx, http.MethodPut, u, r, "application/octet-stream", &it); err != nil {
		return Entry{}, err
	}
	return toEntry(it, relPath), nil
}

// Delete — DELETE auf das Item. Die Freigabe-Wurzel selbst ist tabu.
func (c *Client) Delete(ctx context.Context, root Root, relPath string) error {
	if relPath == "" {
		return fmt.Errorf("der Wurzel-Ordner der Freigabe kann nicht geloescht werden")
	}
	return c.do(ctx, http.MethodDelete, c.itemURL(root, relPath, ""), nil, "", nil)
}

// Mkdir legt einen Ordnerpfad an (mkdir -p): jedes Segment einzeln, bereits
// vorhandene Ordner sind kein Fehler.
func (c *Client) Mkdir(ctx context.Context, root Root, relPath string) (Entry, error) {
	if relPath == "" {
		return Entry{}, fmt.Errorf("pfad fehlt")
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
