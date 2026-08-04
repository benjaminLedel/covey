package sharepoint

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"covey/internal/target"
)

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig("https://contoso.sharepoint.com/:f:/s/TeamX/AbC graph=https://graph.example/ login=https://login.example",
		"tenant1:client1:secret:mit:doppelpunkt")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShareLink != "https://contoso.sharepoint.com/:f:/s/TeamX/AbC" {
		t.Errorf("ShareLink = %q", cfg.ShareLink)
	}
	if cfg.GraphBase != "https://graph.example" || cfg.LoginBase != "https://login.example" {
		t.Errorf("Endpoints = %q / %q", cfg.GraphBase, cfg.LoginBase)
	}
	if cfg.Tenant != "tenant1" || cfg.ClientID != "client1" || cfg.ClientSecret != "secret:mit:doppelpunkt" {
		t.Errorf("triple = %q %q %q", cfg.Tenant, cfg.ClientID, cfg.ClientSecret)
	}

	cfg, err = ParseConfig("https://contoso.sharepoint.com/sites/X/Docs", "roher-bearer-token")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StaticToken != "roher-bearer-token" || cfg.Tenant != "" {
		t.Errorf("static token not recognized: %+v", cfg)
	}
	if cfg.GraphBase != "https://graph.microsoft.com" {
		t.Errorf("GraphBase default = %q", cfg.GraphBase)
	}

	for _, bad := range []struct{ url, token string }{
		{"", "tok"},
		{"kein-link", "tok"},
		{"https://x.example/a extra=unbekannt", "tok"},
		{"https://x.example/a", ""},
		{"https://x.example/a", "tenant::secret"},
	} {
		if _, err := ParseConfig(bad.url, bad.token); err == nil {
			t.Errorf("ParseConfig(%q, %q): expected an error", bad.url, bad.token)
		}
	}
}

func TestCleanRemotePath(t *testing.T) {
	for in, want := range map[string]string{
		"":               "",
		"/":              "",
		"a/b.txt":        "a/b.txt",
		"/a/b/":          "a/b",
		"a\\b\\c.txt":    "a/b/c.txt",
		"a/./b":          "a/b",
		"  ordner/x  ":   "ordner/x",
		"a/zwischen/../": "a",
	} {
		got, err := cleanRemotePath(in)
		if err != nil || got != want {
			t.Errorf("cleanRemotePath(%q) = %q, %v — want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "../x", "a/../../x"} {
		if _, err := cleanRemotePath(bad); err == nil {
			t.Errorf("cleanRemotePath(%q): expected an error", bad)
		}
	}
}

// fakeGraph builds a test server that serves the token endpoint and the Graph
// routes the plugin uses.
func fakeGraph(t *testing.T, tokenCalls *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("POST /tenant1/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		if err := r.ParseForm(); err != nil ||
			r.Form.Get("grant_type") != "client_credentials" ||
			r.Form.Get("client_id") != "client1" ||
			r.Form.Get("client_secret") != "secret1" ||
			!strings.HasSuffix(r.Form.Get("scope"), "/.default") {
			http.Error(w, "bad form: "+r.Form.Encode(), http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{"access_token":"flow-token","expires_in":3600}`)
	})

	mux.HandleFunc("GET /v1.0/shares/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		enc := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.0/shares/u!"), "/driveItem")
		link, _ := base64.URLEncoding.DecodeString(enc + strings.Repeat("=", (4-len(enc)%4)%4))
		if strings.Contains(string(link), "file-link") {
			fmt.Fprint(w, `{"id":"f9","name":"bericht.docx","file":{},"parentReference":{"driveId":"d1"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"root1","name":"Ablage","webUrl":"https://contoso.sharepoint.com/sites/X/Docs","folder":{"childCount":2},"parentReference":{"driveId":"d1"}}`)
	})

	item := func(name string, size int, folder bool) string {
		if folder {
			return fmt.Sprintf(`{"name":%q,"size":%d,"folder":{"childCount":1},"lastModifiedDateTime":"2026-07-01T10:00:00Z"}`, name, size)
		}
		return fmt.Sprintf(`{"name":%q,"size":%d,"file":{},"lastModifiedDateTime":"2026-07-01T10:00:00Z"}`, name, size)
	}

	mux.HandleFunc("GET /v1.0/drives/d1/items/root1/children", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"value":[%s,%s]}`, item("notes.txt", 5, false), item("berichte", 0, true))
	})
	mux.HandleFunc("GET /v1.0/drives/d1/items/root1:/berichte:/children", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"value":[%s],"@odata.nextLink":"https://weiter"}`, item("q3.docx", 9, false))
	})
	mux.HandleFunc("GET /v1.0/drives/d1/items/root1:/notes.txt:/content", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hallo welt")
	})
	mux.HandleFunc("GET /v1.0/drives/d1/items/root1:/blob.bin:/content", func(w http.ResponseWriter, w2 *http.Request) {
		_, _ = w.Write([]byte{0xff, 0xfe, 0x00, 0x01})
	})
	mux.HandleFunc("PUT /v1.0/drives/d1/items/root1:/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":/content") {
			http.NotFound(w, r)
			return
		}
		if r.ContentLength < 0 {
			http.Error(w, "chunked upload — Content-Length missing", http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(r.Body)
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.0/drives/d1/items/root1:/"), ":/content")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, item(name, len(data), false))
	})
	mux.HandleFunc("DELETE /v1.0/drives/d1/items/root1:/old.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1.0/drives/d1/items/root1/children", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name     string          `json:"name"`
			Folder   json.RawMessage `json:"folder"`
			Conflict string          `json:"@microsoft.graph.conflictBehavior"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Folder == nil || body.Conflict != "fail" {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		// The first segment already exists → Graph 409.
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"error":{"code":"nameAlreadyExists","message":"existiert"}}`)
	})
	mux.HandleFunc("POST /v1.0/drives/d1/items/root1:/eingang:/children", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, item(body.Name, 0, true))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// exec runs an action through the System interface — the same path the action
// proxy in the daemon takes.
func exec(t *testing.T, ctx context.Context, cred target.Credential, action, params string) (any, error) {
	t.Helper()
	return System{}.Execute(ctx, action, json.RawMessage(params), cred)
}

func testCred(srv *httptest.Server, link string) target.Credential {
	return target.Credential{
		BaseURL: link + " graph=" + srv.URL + " login=" + srv.URL,
		Token:   "statischer-bearer",
	}
}

func TestExecuteActions(t *testing.T) {
	var tokenCalls atomic.Int32
	srv := fakeGraph(t, &tokenCalls)
	ctx := context.Background()
	cred := testCred(srv, "https://contoso.sharepoint.com/sites/X/ordner-link")

	// list on the root
	out, err := exec(t, ctx, cred, "list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["root"] != "Ablage" || len(m["entries"].([]Entry)) != 2 {
		t.Fatalf("list = %+v", m)
	}

	// list in the subfolder, with the truncated hint
	out, err = exec(t, ctx, cred, "list", `{"path":"berichte"}`)
	if err != nil {
		t.Fatal(err)
	}
	m = out.(map[string]any)
	if m["truncated"] != true {
		t.Errorf("truncated missing: %+v", m)
	}
	if e := m["entries"].([]Entry)[0]; e.Path != "berichte/q3.docx" || e.Type != "file" {
		t.Errorf("entry = %+v", e)
	}

	// read of a text file
	out, err = exec(t, ctx, cred, "read", `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(map[string]any)["content"]; got != "hallo welt" {
		t.Errorf("read = %q", got)
	}

	// read of a binary file → an error carrying the download hint
	if _, err = exec(t, ctx, cred, "read", `{"path":"blob.bin"}`); err == nil || !strings.Contains(err.Error(), "download") {
		t.Errorf("read binary: %v", err)
	}

	// write
	out, err = exec(t, ctx, cred, "write", `{"path":"neu.md","content":"# Hallo"}`)
	if err != nil {
		t.Fatal(err)
	}
	if e := out.(Entry); e.Name != "neu.md" || e.Size != 7 {
		t.Errorf("write = %+v", e)
	}

	// mkdir: the first segment exists (409 tolerated), the second is created
	out, err = exec(t, ctx, cred, "mkdir", `{"path":"eingang/2026"}`)
	if err != nil {
		t.Fatal(err)
	}
	if e := out.(Entry); e.Path != "eingang/2026" || e.Type != "folder" {
		t.Errorf("mkdir = %+v", e)
	}

	// delete
	if _, err = exec(t, ctx, cred, "delete", `{"path":"old.txt"}`); err != nil {
		t.Fatal(err)
	}
	// deleting the root is off limits
	if _, err = exec(t, ctx, cred, "delete", `{"path":""}`); err == nil {
		t.Error("delete of the root: expected an error")
	}

	// Path breakout, remote side
	if _, err = exec(t, ctx, cred, "read", `{"path":"../geheim.txt"}`); err == nil {
		t.Error("remote traversal: expected an error")
	}

	// Unknown action
	if _, err = exec(t, ctx, cred, "kaputt", `{}`); err == nil {
		t.Error("unknown action: expected an error")
	}
	if tokenCalls.Load() != 0 {
		t.Errorf("a static token must not kick off the OAuth flow (calls=%d)", tokenCalls.Load())
	}
}

func TestExecuteSandboxTransfer(t *testing.T) {
	var tokenCalls atomic.Int32
	srv := fakeGraph(t, &tokenCalls)
	workdir := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), workdir)
	cred := testCred(srv, "https://contoso.sharepoint.com/sites/X/ordner-link")

	// download into the sandbox (default target path)
	out, err := exec(t, ctx, cred, "download", `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	local := out.(map[string]any)["path"].(string)
	if want := filepath.Join(workdir, "sharepoint", "notes.txt"); local != want {
		t.Errorf("download to %q, want %q", local, want)
	}
	if data, _ := os.ReadFile(local); string(data) != "hallo welt" {
		t.Errorf("download content = %q", data)
	}

	// upload out of the sandbox
	src := filepath.Join(workdir, "ergebnis.txt")
	if err := os.WriteFile(src, []byte("fertig"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = exec(t, ctx, cred, "upload", `{"from":"ergebnis.txt","to":"berichte/ergebnis.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if e := out.(Entry); e.Name != "berichte/ergebnis.txt" && e.Path != "berichte/ergebnis.txt" {
		t.Errorf("upload = %+v", e)
	}

	// Breaking out of the workdir
	if _, err = exec(t, ctx, cred, "upload", `{"from":"../../etc/passwd","to":"x"}`); err == nil {
		t.Error("upload traversal: expected an error")
	}
	if _, err = exec(t, ctx, cred, "download", `{"path":"notes.txt","to":"../ausbruch.txt"}`); err == nil {
		t.Error("download traversal: expected an error")
	}

	// No transfer without a sandbox workdir
	if _, err = exec(t, context.Background(), cred, "download", `{"path":"notes.txt"}`); err == nil {
		t.Error("download without a workdir: expected an error")
	}
}

func TestExecuteClientCredentialsFlow(t *testing.T) {
	var tokenCalls atomic.Int32
	srv := fakeGraph(t, &tokenCalls)
	ctx := context.Background()
	cred := target.Credential{
		BaseURL: "https://contoso.sharepoint.com/sites/X/ordner-link graph=" + srv.URL + " login=" + srv.URL,
		Token:   "tenant1:client1:secret1",
	}

	if _, err := exec(t, ctx, cred, "list", `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(t, ctx, cred, "list", `{"path":"berichte"}`); err != nil {
		t.Fatal(err)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Errorf("token endpoint called %d times, want 1 (cache)", got)
	}
}

func TestResolveRootRejectsFileLink(t *testing.T) {
	var tokenCalls atomic.Int32
	srv := fakeGraph(t, &tokenCalls)
	cred := testCred(srv, "https://contoso.sharepoint.com/sites/X/file-link")
	if _, err := exec(t, context.Background(), cred, "list", `{}`); err == nil ||
		!strings.Contains(err.Error(), "folder") {
		t.Errorf("a file link must be rejected: %v", err)
	}
}

func TestActionSubjectAndDocs(t *testing.T) {
	if got := (System{}).ActionSubject("delete", nil); got != "sharepoint:delete" {
		t.Errorf("ActionSubject = %q", got)
	}
	doc := (System{}).PromptDoc()
	for _, a := range []string{"list", "read", "write", "download", "upload", "mkdir", "delete"} {
		if !strings.Contains(doc, a+" {") {
			t.Errorf("PromptDoc without the action %q", a)
		}
	}
	if ok := (System{}).VerifyWebhook("s", nil, nil); ok {
		t.Error("VerifyWebhook must return false (no webhook)")
	}
	if _, err := (System{}).ParseWebhook(nil); err == nil {
		t.Error("ParseWebhook must return an error (no webhook)")
	}
}
