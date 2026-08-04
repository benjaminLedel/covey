package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	identbuiltin "covey/internal/identity/builtin"
)

// TestSandboxFilesAPI checks an agent's workplace through the API (spec/02:
// persistent home): upload, list, read, change, download, rename, delete —
// including RBAC and the rule that no path leads out of the home.
func TestSandboxFilesAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "file-agent", "display_name": "Datei-Agent", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	base := "/api/v1/agents/" + agentID + "/files"

	// An agent that was never woken has no home yet: empty, but not an error.
	leer := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	if leer["exists"] != false {
		t.Fatalf("a fresh home should return exists=false: %+v", leer)
	}

	// Uploading creates the home.
	admin.upload(t, base+"/upload?path=notizen", map[string]string{
		"kunde.md":  "# ACME\n\nNur telefonisch erreichbar.",
		"liste.txt": "eins\nzwei\n",
	})

	list := admin.expect(http.MethodGet, base+"?path=notizen", nil, http.StatusOK)
	namen := entryNames(list)
	if len(namen) != 2 || namen["kunde.md"] == nil || namen["liste.txt"] == nil {
		t.Fatalf("uploaded files are missing: %+v", list)
	}

	// Reading returns the content as text.
	f := admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("notizen/kunde.md"), nil, http.StatusOK)
	if f["binary"] != false || f["content"] != "# ACME\n\nNur telefonisch erreichbar." {
		t.Fatalf("unexpected content: %+v", f)
	}

	// Change and read again.
	admin.expect(http.MethodPut, base+"/content",
		map[string]string{"path": "notizen/kunde.md", "content": "# ACME\n\nJetzt per Mail."}, http.StatusOK)
	f = admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("notizen/kunde.md"), nil, http.StatusOK)
	if f["content"] != "# ACME\n\nJetzt per Mail." {
		t.Fatalf("the change was not stored: %+v", f)
	}

	// Downloading returns the bytes as an attachment.
	resp := admin.do(http.MethodGet, base+"/download?path="+url.QueryEscape("notizen/liste.txt"), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "eins\nzwei\n" {
		t.Fatalf("download: HTTP %d, %q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" || cd[:10] != "attachment" {
		t.Fatalf("a download must be an attachment, got %q", cd)
	}

	// Create, move and delete a folder.
	admin.expect(http.MethodPost, base+"/dir", map[string]string{"path": "archiv"}, http.StatusCreated)
	admin.expect(http.MethodPost, base+"/move",
		map[string]string{"from": "notizen/liste.txt", "to": "archiv/liste.txt"}, http.StatusOK)
	admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("notizen/liste.txt"), nil, http.StatusNotFound)
	admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("archiv/liste.txt"), nil, http.StatusOK)

	admin.expect(http.MethodDelete, base+"?path=archiv", nil, http.StatusOK)
	admin.expect(http.MethodGet, base+"?path=archiv", nil, http.StatusNotFound)

	// No way out of the home — neither reading nor writing.
	admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("../../etc/passwd"), nil, http.StatusNotFound)
	admin.expect(http.MethodDelete, base+"?path=", nil, http.StatusBadRequest)

	// Every change is in the recording — with the human who made it.
	var events []map[string]any
	resp = admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/recording", nil)
	json.NewDecoder(resp.Body).Decode(&events)
	resp.Body.Close()
	var dateiEvents int
	for _, e := range events {
		if e["kind"] != "file" {
			continue
		}
		dateiEvents++
		p, _ := e["payload"].(map[string]any)
		if p["actor"] != "Admin" {
			t.Fatalf("file event without an acting human: %+v", p)
		}
	}
	if dateiEvents == 0 {
		t.Fatal("file changes must be in the recording")
	}

	// RBAC: the auditor does not see the workplace, security reads but does not write.
	ctx := context.Background()
	for email, role := range map[string]string{"auditor@test.local": "auditor", "sec@test.local": "security"} {
		hash, _ := identbuiltin.HashPassword("passwort-1234")
		if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
			VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New(), s.orgID, email, role, hash, role); err != nil {
			t.Fatal(err)
		}
	}
	auditor := login(t, s, "auditor@test.local", "passwort-1234")
	auditor.expect(http.MethodGet, base, nil, http.StatusForbidden)

	security := login(t, s, "sec@test.local", "passwort-1234")
	security.expect(http.MethodGet, base, nil, http.StatusOK)
	security.expect(http.MethodPut, base+"/content",
		map[string]string{"path": "notizen/kunde.md", "content": "manipuliert"}, http.StatusForbidden)

	// And none of this reaches into a foreign home: an agent of another
	// organization simply does not exist for this session.
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	fremd, err := s.registry.Create(ctx, fremdeOrg, "fremd", "Fremd", "mock", nil)
	if err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodGet, "/api/v1/agents/"+fremd.ID.String()+"/files", nil, http.StatusNotFound)
}

// TestSandboxFilePreview checks the inline preview (spec/02): images and PDF
// come inline with their real type, everything else not at all — the allowlist
// is the bolt against executing foreign HTML on the Covey origin.
func TestSandboxFilePreview(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "vorschau-agent", "display_name": "Vorschau", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	base := "/api/v1/agents/" + agentID + "/files"

	// A tiny, valid PNG (1×1, transparent).
	png, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	admin.uploadRaw(t, base+"/upload?path=bilder", "punkt.png", png)
	admin.upload(t, base+"/upload?path=bilder", map[string]string{
		"seite.html": "<script>alert(1)</script>",
		"notiz.md":   "# Titel",
	})

	// The listing carries the preview kind — the icon in the UI hangs off it.
	list := admin.expect(http.MethodGet, base+"?path=bilder", nil, http.StatusOK)
	arten := map[string]any{}
	for name, e := range entryNames(list) {
		arten[name] = e.(map[string]any)["preview"]
	}
	if arten["punkt.png"] != "image" || arten["notiz.md"] != "markdown" {
		t.Fatalf("unexpected preview kinds: %v", arten)
	}

	// An image comes inline, with its real type and without sniffing.
	resp := admin.do(http.MethodGet, base+"/preview?path="+url.QueryEscape("bilder/punkt.png"), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, png) {
		t.Fatalf("image preview: HTTP %d, %d bytes", resp.StatusCode, len(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type %q, expected image/png", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing — the browser would otherwise guess the type itself")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("image without a sandbox CSP: %q", csp)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("content-disposition %q, expected inline", cd)
	}

	// HTML and Markdown are NOT served inline — fail-closed via the allowlist.
	for _, p := range []string{"bilder/seite.html", "bilder/notiz.md"} {
		resp := admin.do(http.MethodGet, base+"/preview?path="+url.QueryEscape(p), nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%s: HTTP %d, expected 415", p, resp.StatusCode)
		}
	}

	// The download stays the way for everything — and always as an attachment.
	resp = admin.do(http.MethodGet, base+"/download?path="+url.QueryEscape("bilder/seite.html"), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") ||
		resp.Header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("an html download must be an attachment with a neutral type: %d %q %q", resp.StatusCode,
			resp.Header.Get("Content-Disposition"), resp.Header.Get("Content-Type"))
	}
}

// uploadRaw uploads a single file with arbitrary bytes.
func (c *apiClient) uploadRaw(t *testing.T, path, name string, content []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload %s: HTTP %d: %s", name, resp.StatusCode, b)
	}
}

// TestSandboxFilesZipUndOrdnerUpload checks the bulk paths (spec/02): an upload
// with directory parts in the file name creates the structure, and the ZIP
// endpoint returns several paths — whole folders as well — in one archive.
func TestSandboxFilesZipUndOrdnerUpload(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "zip-agent", "display_name": "ZIP", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	base := "/api/v1/agents/" + agentID + "/files"

	// A folder upload: the file name carries the relative path — that is
	// exactly how the browser sends it when a folder is dragged in.
	admin.upload(t, base+"/upload?path=projekt", map[string]string{
		"doku/kapitel-1.md":      "# Eins",
		"doku/bilder/skizze.txt": "skizze",
		"liesmich.txt":           "hallo",
	})

	// The structure is created, not flattened.
	list := admin.expect(http.MethodGet, base+"?path="+url.QueryEscape("projekt/doku/bilder"), nil, http.StatusOK)
	if entryNames(list)["skizze.txt"] == nil {
		t.Fatalf("the nested upload is missing: %+v", list)
	}

	// A file name containing `..` does not break out: it is normalized BEFORE
	// it is appended to the target directory — so the file lands in the target,
	// not one level above it and certainly not outside the home.
	admin.upload(t, base+"/upload?path=projekt", map[string]string{"../../entwischt.txt": "nein"})
	admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("projekt/entwischt.txt"), nil, http.StatusOK)
	root := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	if entryNames(root)["entwischt.txt"] != nil {
		t.Fatalf("a `..` in the file name must not write one level up: %+v", root)
	}

	// ZIP over several paths: one folder and one file.
	resp := admin.do(http.MethodGet, base+"/zip?path="+url.QueryEscape("projekt/doku")+
		"&path="+url.QueryEscape("projekt/liesmich.txt"), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("zip: HTTP %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content-type %q, expected application/zip", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("a zip must be an attachment: %q", cd)
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("archive unreadable: %v", err)
	}
	drin := map[string]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		drin[f.Name] = string(b)
	}
	if drin["doku/kapitel-1.md"] != "# Eins" || drin["doku/bilder/skizze.txt"] != "skizze" ||
		drin["liesmich.txt"] != "hallo" {
		t.Fatalf("unexpected archive content: %v", drin)
	}

	// Without a path no archive, and here too nobody gets out of the home.
	admin.expect(http.MethodGet, base+"/zip", nil, http.StatusBadRequest)
	admin.expect(http.MethodGet, base+"/zip?path="+url.QueryEscape("../../etc"), nil, http.StatusNotFound)

	// Security may read, so it may download too — writing still not.
	ctx := context.Background()
	hash, _ := identbuiltin.HashPassword("passwort-1234")
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'zipsec@test.local','Security',$3,'security')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}
	security := login(t, s, "zipsec@test.local", "passwort-1234")
	security.expect(http.MethodGet, base+"/zip?path="+url.QueryEscape("projekt"), nil, http.StatusOK)
}

// upload sends files as multipart/form-data, the way the browser does.
func (c *apiClient) upload(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		w, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, c.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload: HTTP %d: %s", resp.StatusCode, body)
	}
}

// entryNames splits the entries of a listing apart by name.
func entryNames(listing map[string]any) map[string]any {
	out := map[string]any{}
	entries, _ := listing["entries"].([]any)
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if m != nil {
			out[m["name"].(string)] = m
		}
	}
	return out
}
