package integration

import (
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

// TestSandboxFilesAPI prüft den Arbeitsplatz eines Agenten über die API
// (spec/02: persistentes Home): hochladen, auflisten, lesen, ändern,
// herunterladen, umbenennen, löschen — samt RBAC und der Regel, dass kein Pfad
// aus dem Home herausführt.
func TestSandboxFilesAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "datei-agent", "display_name": "Datei-Agent", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	base := "/api/v1/agents/" + agentID + "/files"

	// Ein nie geweckter Agent hat noch kein Home: leer, aber kein Fehler.
	leer := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	if leer["exists"] != false {
		t.Fatalf("frisches home müsste exists=false liefern: %+v", leer)
	}

	// Hochladen legt das Home an.
	admin.upload(t, base+"/upload?path=notizen", map[string]string{
		"kunde.md":  "# ACME\n\nNur telefonisch erreichbar.",
		"liste.txt": "eins\nzwei\n",
	})

	list := admin.expect(http.MethodGet, base+"?path=notizen", nil, http.StatusOK)
	namen := entryNames(list)
	if len(namen) != 2 || namen["kunde.md"] == nil || namen["liste.txt"] == nil {
		t.Fatalf("hochgeladene dateien fehlen: %+v", list)
	}

	// Lesen liefert den Inhalt als Text.
	f := admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("notizen/kunde.md"), nil, http.StatusOK)
	if f["binary"] != false || f["content"] != "# ACME\n\nNur telefonisch erreichbar." {
		t.Fatalf("inhalt unerwartet: %+v", f)
	}

	// Ändern und wieder lesen.
	admin.expect(http.MethodPut, base+"/content",
		map[string]string{"path": "notizen/kunde.md", "content": "# ACME\n\nJetzt per Mail."}, http.StatusOK)
	f = admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("notizen/kunde.md"), nil, http.StatusOK)
	if f["content"] != "# ACME\n\nJetzt per Mail." {
		t.Fatalf("änderung nicht gespeichert: %+v", f)
	}

	// Herunterladen liefert die Bytes als Anhang.
	resp := admin.do(http.MethodGet, base+"/download?path="+url.QueryEscape("notizen/liste.txt"), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "eins\nzwei\n" {
		t.Fatalf("download: HTTP %d, %q", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" || cd[:10] != "attachment" {
		t.Fatalf("download muss ein anhang sein, got %q", cd)
	}

	// Ordner anlegen, verschieben, löschen.
	admin.expect(http.MethodPost, base+"/dir", map[string]string{"path": "archiv"}, http.StatusCreated)
	admin.expect(http.MethodPost, base+"/move",
		map[string]string{"from": "notizen/liste.txt", "to": "archiv/liste.txt"}, http.StatusOK)
	admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("notizen/liste.txt"), nil, http.StatusNotFound)
	admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("archiv/liste.txt"), nil, http.StatusOK)

	admin.expect(http.MethodDelete, base+"?path=archiv", nil, http.StatusOK)
	admin.expect(http.MethodGet, base+"?path=archiv", nil, http.StatusNotFound)

	// Kein Weg aus dem Home heraus — weder lesend noch schreibend.
	admin.expect(http.MethodGet, base+"/content?path="+url.QueryEscape("../../etc/passwd"), nil, http.StatusNotFound)
	admin.expect(http.MethodDelete, base+"?path=", nil, http.StatusBadRequest)

	// Jede Änderung steht im Recording — mit dem Menschen, der sie gemacht hat.
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
			t.Fatalf("datei-event ohne handelnden menschen: %+v", p)
		}
	}
	if dateiEvents == 0 {
		t.Fatal("datei-änderungen müssen im recording stehen")
	}

	// RBAC: Auditor sieht den Arbeitsplatz nicht, Security liest, schreibt aber nicht.
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

	// Und nichts davon reicht in ein fremdes Home: ein Agent einer anderen
	// Organisation ist für diese Session schlicht nicht vorhanden.
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

// TestSandboxFilePreview prüft die Inline-Vorschau (spec/02): Bilder und PDF
// kommen mit ihrem echten Typ inline, alles andere gar nicht — die Allowlist
// ist der Riegel dagegen, fremdes HTML auf der Covey-Origin auszuführen.
func TestSandboxFilePreview(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "vorschau-agent", "display_name": "Vorschau", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	base := "/api/v1/agents/" + agentID + "/files"

	// Ein winziges, gültiges PNG (1×1, transparent).
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

	// Die Auflistung trägt die Vorschau-Art — daran hängt das Symbol in der UI.
	list := admin.expect(http.MethodGet, base+"?path=bilder", nil, http.StatusOK)
	arten := map[string]any{}
	for name, e := range entryNames(list) {
		arten[name] = e.(map[string]any)["preview"]
	}
	if arten["punkt.png"] != "image" || arten["notiz.md"] != "markdown" {
		t.Fatalf("vorschau-arten unerwartet: %v", arten)
	}

	// Ein Bild kommt inline, mit echtem Typ und ohne Sniffing.
	resp := admin.do(http.MethodGet, base+"/preview?path="+url.QueryEscape("bilder/punkt.png"), nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, png) {
		t.Fatalf("bild-vorschau: HTTP %d, %d bytes", resp.StatusCode, len(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type %q, erwartet image/png", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff fehlt — der Browser dürfte den Typ sonst selbst raten")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("bild ohne sandbox-CSP: %q", csp)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("content-disposition %q, erwartet inline", cd)
	}

	// HTML und Markdown gibt es NICHT inline — fail-closed über die Allowlist.
	for _, p := range []string{"bilder/seite.html", "bilder/notiz.md"} {
		resp := admin.do(http.MethodGet, base+"/preview?path="+url.QueryEscape(p), nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%s: HTTP %d, erwartet 415", p, resp.StatusCode)
		}
	}

	// Der Download bleibt für alles der Weg — und immer als Anhang.
	resp = admin.do(http.MethodGet, base+"/download?path="+url.QueryEscape("bilder/seite.html"), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") ||
		resp.Header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("html-download muss ein anhang mit neutralem typ sein: %d %q %q", resp.StatusCode,
			resp.Header.Get("Content-Disposition"), resp.Header.Get("Content-Type"))
	}
}

// uploadRaw lädt eine einzelne Datei mit beliebigen Bytes hoch.
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

// upload schickt Dateien als multipart/form-data, wie es der Browser tut.
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

// entryNames zieht die Einträge einer Auflistung nach Namen auseinander.
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
