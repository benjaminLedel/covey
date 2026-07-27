package nextcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"covey/internal/target"
)

func TestParseConfig(t *testing.T) {
	// Freigabelink mit Passwort.
	cfg, err := ParseConfig("https://cloud.example.com/s/AbCdEf", "geheim")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Share || cfg.DavBase != "https://cloud.example.com/public.php/webdav/" ||
		cfg.User != "AbCdEf" || cfg.Pass != "geheim" {
		t.Errorf("Freigabelink falsch geparst: %+v", cfg)
	}

	// Freigabelink ohne Passwort (Sentinel) + /index.php + Installations-Präfix.
	cfg, err = ParseConfig("https://cloud.example.com/nc/index.php/s/XyZ/download", "-")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DavBase != "https://cloud.example.com/nc/public.php/webdav/" || cfg.User != "XyZ" || cfg.Pass != "" {
		t.Errorf("Freigabelink ohne Passwort falsch: %+v", cfg)
	}

	// Konto-Zugang: Server-Basis + benutzer:app-passwort.
	cfg, err = ParseConfig("https://cloud.example.com", "alice:app-pass:mit:doppelpunkt")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Share || cfg.DavBase != "https://cloud.example.com/remote.php/dav/files/alice/" ||
		cfg.User != "alice" || cfg.Pass != "app-pass:mit:doppelpunkt" {
		t.Errorf("Konto-Zugang falsch: %+v", cfg)
	}

	// Konto-Zugang: eine mitgegebene remote.php-URL wird auf den Datei-Root normalisiert.
	cfg, _ = ParseConfig("https://cloud.example.com/remote.php/dav/files/bob/", "bob:pw")
	if cfg.DavBase != "https://cloud.example.com/remote.php/dav/files/bob/" {
		t.Errorf("remote.php-URL falsch normalisiert: %q", cfg.DavBase)
	}

	for _, bad := range []struct{ url, token string }{
		{"", "tok"},
		{"kein-link", "tok"},
		{"https://cloud.example.com", "ohne-doppelpunkt"},
		{"https://cloud.example.com", "user:"},
		{"https://cloud.example.com", ":pass"},
	} {
		if _, err := ParseConfig(bad.url, bad.token); err == nil {
			t.Errorf("ParseConfig(%q, %q): Fehler erwartet", bad.url, bad.token)
		}
	}
}

func TestCleanRemotePath(t *testing.T) {
	for in, want := range map[string]string{
		"":            "",
		"/":           "",
		"a/b.txt":     "a/b.txt",
		"/a/b/":       "a/b",
		"a\\b\\c.txt": "a/b/c.txt",
		"a/./b":       "a/b",
		"  ordner/x ": "ordner/x",
	} {
		got, err := cleanRemotePath(in)
		if err != nil || got != want {
			t.Errorf("cleanRemotePath(%q) = %q, %v — will %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "../x", "a/../../x"} {
		if _, err := cleanRemotePath(bad); err == nil {
			t.Errorf("cleanRemotePath(%q): Fehler erwartet", bad)
		}
	}
}

// fakeDav baut einen WebDAV-Server, der die vom Plugin genutzten Methoden über
// dem public.php-Wurzelpfad bedient. Er hält einen kleinen In-Memory-Baum.
func fakeDav(t *testing.T) *httptest.Server {
	t.Helper()
	const root = "/public.php/webdav/"
	files := map[string][]byte{
		"notes.txt": []byte("hallo welt"),
		"blob.bin":  {0xff, 0xfe, 0x00, 0x01},
	}
	dirs := map[string]bool{"berichte": true}

	propResp := func(href, name string, size int, dir bool) string {
		rt := ""
		length := fmt.Sprintf("<d:getcontentlength>%d</d:getcontentlength>", size)
		if dir {
			rt = "<d:collection/>"
			length = ""
		}
		return fmt.Sprintf(`<d:response><d:href>%s</d:href><d:propstat>`+
			`<d:prop><d:displayname>%s</d:displayname><d:resourcetype>%s</d:resourcetype>%s`+
			`<d:getlastmodified>Mon, 01 Jul 2026 10:00:00 GMT</d:getlastmodified></d:prop>`+
			`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, href, name, rt, length)
	}

	h := func(w http.ResponseWriter, r *http.Request) {
		// Basic-Auth muss anliegen (Share-Token als User).
		if u, _, ok := r.BasicAuth(); !ok || u == "" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		rel := strings.Trim(strings.TrimPrefix(r.URL.Path, root), "/")
		switch r.Method {
		case "PROPFIND":
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
			if rel == "" {
				b.WriteString(propResp(root, "Freigabe-Wurzel", 0, true))
				b.WriteString(propResp(root+"notes.txt", "notes.txt", 10, false))
				b.WriteString(propResp(root+"blob.bin", "blob.bin", 4, false))
				b.WriteString(propResp(root+"berichte", "berichte", 0, true))
			} else if rel == "berichte" {
				b.WriteString(propResp(root+"berichte/", "berichte", 0, true))
				b.WriteString(propResp(root+"berichte/q3.pdf", "q3.pdf", 9, false))
			} else {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			b.WriteString(`</d:multistatus>`)
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, b.String())
		case http.MethodGet:
			data, ok := files[rel]
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Write(data)
		case http.MethodPut:
			// PUT in einen nicht existenten Ordner → 409 (Nextcloud-Verhalten).
			if parent := filepath.ToSlash(filepath.Dir(rel)); parent != "." && !dirs[parent] {
				http.Error(w, "conflict", http.StatusConflict)
				return
			}
			data, _ := io.ReadAll(r.Body)
			files[rel] = data
			w.WriteHeader(http.StatusCreated)
		case "MKCOL":
			dirs[rel] = true
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			delete(files, rel)
			delete(dirs, rel)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(h))
	t.Cleanup(srv.Close)
	return srv
}

func exec(t *testing.T, ctx context.Context, cred target.Credential, action, params string) (any, error) {
	t.Helper()
	return System{}.Execute(ctx, action, json.RawMessage(params), cred)
}

// shareCred baut ein Credential im Freigabelink-Modus gegen den Testserver.
func shareCred(srv *httptest.Server) target.Credential {
	return target.Credential{BaseURL: srv.URL + "/s/AbCdEf", Token: "geheim"}
}

func TestExecuteActions(t *testing.T) {
	srv := fakeDav(t)
	ctx := context.Background()
	cred := shareCred(srv)

	// list auf der Wurzel: 3 Kinder, Wurzel-Eintrag selbst nicht gelistet.
	out, err := exec(t, ctx, cred, "list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["root"] != "Freigabe-Wurzel" {
		t.Errorf("root = %v", m["root"])
	}
	entries := m["entries"].([]Entry)
	if len(entries) != 3 {
		t.Fatalf("list = %+v", entries)
	}

	// list im Unterordner.
	out, err = exec(t, ctx, cred, "list", `{"path":"berichte"}`)
	if err != nil {
		t.Fatal(err)
	}
	e := out.(map[string]any)["entries"].([]Entry)
	if len(e) != 1 || e[0].Path != "berichte/q3.pdf" || e[0].Type != "file" || e[0].Size != 9 {
		t.Errorf("unterordner-list = %+v", e)
	}

	// read einer Textdatei.
	out, err = exec(t, ctx, cred, "read", `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(map[string]any)["content"]; got != "hallo welt" {
		t.Errorf("read = %q", got)
	}

	// read einer Binärdatei → Fehler mit download-Hinweis.
	if _, err = exec(t, ctx, cred, "read", `{"path":"blob.bin"}`); err == nil || !strings.Contains(err.Error(), "download") {
		t.Errorf("read binaer: %v", err)
	}

	// write (Zielordner existiert).
	out, err = exec(t, ctx, cred, "write", `{"path":"berichte/neu.md","content":"# Hallo"}`)
	if err != nil {
		t.Fatal(err)
	}
	if e := out.(Entry); e.Name != "neu.md" || e.Size != 7 {
		t.Errorf("write = %+v", e)
	}

	// write in nicht existenten Ordner → auto-mkdir des Parents, dann PUT.
	if _, err = exec(t, ctx, cred, "write", `{"path":"frisch/tief/x.txt","content":"ok"}`); err != nil {
		t.Fatalf("write mit auto-mkdir: %v", err)
	}

	// mkdir.
	out, err = exec(t, ctx, cred, "mkdir", `{"path":"eingang/2026"}`)
	if err != nil {
		t.Fatal(err)
	}
	if e := out.(Entry); e.Path != "eingang/2026" || e.Type != "folder" {
		t.Errorf("mkdir = %+v", e)
	}

	// delete.
	if _, err = exec(t, ctx, cred, "delete", `{"path":"notes.txt"}`); err != nil {
		t.Fatal(err)
	}
	// delete der Wurzel ist tabu.
	if _, err = exec(t, ctx, cred, "delete", `{"path":""}`); err == nil {
		t.Error("delete der Wurzel: Fehler erwartet")
	}

	// Pfad-Ausbruch remote.
	if _, err = exec(t, ctx, cred, "read", `{"path":"../geheim.txt"}`); err == nil {
		t.Error("remote-Traversal: Fehler erwartet")
	}

	// unbekannte Aktion.
	if _, err = exec(t, ctx, cred, "kaputt", `{}`); err == nil {
		t.Error("unbekannte Aktion: Fehler erwartet")
	}
}

func TestExecuteSandboxTransfer(t *testing.T) {
	srv := fakeDav(t)
	workdir := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), workdir)
	cred := shareCred(srv)

	// download in die Sandbox (Default-Zielpfad).
	out, err := exec(t, ctx, cred, "download", `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	local := out.(map[string]any)["path"].(string)
	if want := filepath.Join(workdir, "nextcloud", "notes.txt"); local != want {
		t.Errorf("download nach %q, will %q", local, want)
	}
	if data, _ := os.ReadFile(local); string(data) != "hallo welt" {
		t.Errorf("download-Inhalt = %q", data)
	}

	// upload aus der Sandbox.
	src := filepath.Join(workdir, "ergebnis.txt")
	if err := os.WriteFile(src, []byte("fertig"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = exec(t, ctx, cred, "upload", `{"from":"ergebnis.txt","to":"berichte/ergebnis.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if e := out.(Entry); e.Path != "berichte/ergebnis.txt" {
		t.Errorf("upload = %+v", e)
	}

	// Ausbruch aus dem Workdir.
	if _, err = exec(t, ctx, cred, "upload", `{"from":"../../etc/passwd","to":"x"}`); err == nil {
		t.Error("upload-Traversal: Fehler erwartet")
	}
	if _, err = exec(t, ctx, cred, "download", `{"path":"notes.txt","to":"../ausbruch.txt"}`); err == nil {
		t.Error("download-Traversal: Fehler erwartet")
	}

	// ohne Sandbox-Workdir kein Transfer.
	if _, err = exec(t, context.Background(), cred, "download", `{"path":"notes.txt"}`); err == nil {
		t.Error("download ohne Workdir: Fehler erwartet")
	}
}

// TestAccountMode prüft, dass der Konto-Zugang denselben WebDAV-Client
// erzeugt — der Testserver bedient den public.php-Pfad, deshalb genügt hier
// die Config-Prüfung; die Aktionen selbst sind pfad-agnostisch (oben getestet).
func TestAccountModeConfig(t *testing.T) {
	cfg, err := ParseConfig("https://cloud.example.com/", "alice:pw")
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(cfg)
	if got := c.itemURL("a/b c.txt", false); got != "https://cloud.example.com/remote.php/dav/files/alice/a/b%20c.txt" {
		t.Errorf("itemURL account = %q", got)
	}
	if got := c.relFromHref("/remote.php/dav/files/alice/a/b%20c.txt"); got != "a/b c.txt" {
		t.Errorf("relFromHref = %q", got)
	}
}

func TestActionSubjectAndDocs(t *testing.T) {
	if got := (System{}).ActionSubject("delete", nil); got != "nextcloud:delete" {
		t.Errorf("ActionSubject = %q", got)
	}
	doc := (System{}).PromptDoc()
	for _, a := range []string{"list", "read", "write", "download", "upload", "mkdir", "delete"} {
		if !strings.Contains(doc, a+" {") {
			t.Errorf("PromptDoc ohne Aktion %q", a)
		}
	}
	if (System{}).VerifyWebhook("s", nil, nil) {
		t.Error("VerifyWebhook muss false liefern (kein Webhook)")
	}
	if _, err := (System{}).ParseWebhook(nil); err == nil {
		t.Error("ParseWebhook muss einen Fehler liefern (kein Webhook)")
	}
}
