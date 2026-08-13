package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

func TestCleanURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/a": "https://example.com/a",
		"example.com":           "https://example.com",
		" http://x.test ":       "http://x.test",
	} {
		got, err := cleanURL(in)
		if err != nil || got != want {
			t.Errorf("cleanURL(%q) = %q, %v — want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "file:///etc/passwd", "ftp://x", "javascript:alert(1)"} {
		if _, err := cleanURL(bad); err == nil {
			t.Errorf("cleanURL(%q): expected an error", bad)
		}
	}
}

func TestActionSubjectAndDocs(t *testing.T) {
	if got := (System{}).ActionSubject("click", nil); got != "browser:click" {
		t.Errorf("ActionSubject = %q", got)
	}
	doc := (System{}).PromptDoc()
	for _, a := range []string{"navigate", "content", "screenshot", "click", "type"} {
		if !strings.Contains(doc, a+" {") {
			t.Errorf("PromptDoc without action %q", a)
		}
	}
	// Kein Webhook-Eingang — und das heisst: die Schnittstelle wird gar nicht
	// erst erfuellt. Vorher stand hier, dass die Ruempfe ablehnen; genau daran
	// erkannte die Einrichtung faelschlich eine Faehigkeit und baute einen
	// Webhook-Schritt, dessen Adresse ins Leere zeigte.
	if _, hook := any(System{}).(target.Webhooker); hook {
		t.Error("browser nimmt keine Webhooks an und darf target.Webhooker nicht erfuellen")
	}
}

func TestParseHasText(t *testing.T) {
	cases := []struct {
		sel, css, needle string
		ok               bool
	}{
		{`button:has-text("Anmelden")`, "button", "Anmelden", true},
		{`a.btn:has-text('Weiter')`, "a.btn", "Weiter", true},
		{`:has-text("Nur Text")`, "*", "Nur Text", true},
		{`button:has-text(  "mit Spaces"  )`, "button", "mit Spaces", true},
		{`button.primary`, "", "", false},
		{`#id`, "", "", false},
	}
	for _, c := range cases {
		css, needle, ok := parseHasText(c.sel)
		if ok != c.ok || (ok && (css != c.css || needle != c.needle)) {
			t.Errorf("parseHasText(%q) = (%q,%q,%v) — want (%q,%q,%v)", c.sel, css, needle, ok, c.css, c.needle, c.ok)
		}
	}
}

// findChromium looks for a Chromium/Chrome for the end-to-end test; if it is
// missing, the browser-driving tests are skipped (like integration tests
// without the dev DB).
func findChromium(t *testing.T) string {
	t.Helper()
	if p := strings.TrimSpace(os.Getenv("COVEY_BROWSER_CHROME_PATH")); p != "" {
		return p
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no Chromium found — end-to-end browser test skipped")
	return ""
}

const testPage = `<!doctype html><html><head><title>Testseite</title></head><body>
<h1 id="head">Hallo Welt</h1>
<button id="btn" onclick="document.getElementById('out').innerText='geklickt'">Klick</button>
<div id="out">initial</div>
<input id="inp" oninput="document.getElementById('echo').innerText=this.value">
<div id="echo"></div>
</body></html>`

func exec2(t *testing.T, ctx context.Context, action, params string) (any, error) {
	t.Helper()
	return System{}.Execute(ctx, action, json.RawMessage(params), target.Credential{})
}

func TestBrowserEndToEnd(t *testing.T) {
	chrome := findChromium(t)
	t.Setenv("COVEY_BROWSER_CHROME_PATH", chrome)
	t.Cleanup(super.shutdown)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(testPage))
	}))
	t.Cleanup(srv.Close)

	workdir := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), workdir)

	// navigate
	out, err := exec2(t, ctx, "navigate", `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if m := out.(map[string]any); m["title"] != "Testseite" {
		t.Errorf("navigate = %+v", m)
	}

	// content of the whole page
	out, err = exec2(t, ctx, "content", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if txt := out.(map[string]any)["text"].(string); !strings.Contains(txt, "Hallo Welt") {
		t.Errorf("content = %q", txt)
	}

	// content via selector
	out, err = exec2(t, ctx, "content", `{"selector":"#head"}`)
	if err != nil {
		t.Fatal(err)
	}
	if txt := out.(map[string]any)["text"].(string); strings.TrimSpace(txt) != "Hallo Welt" {
		t.Errorf("content selector = %q", txt)
	}

	// screenshot into the sandbox
	out, err = exec2(t, ctx, "screenshot", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	shot := out.(map[string]any)["path"].(string)
	if !strings.HasPrefix(shot, filepath.Join(workdir, "browser")) {
		t.Errorf("screenshot path = %q", shot)
	}
	if fi, err := os.Stat(shot); err != nil || fi.Size() == 0 {
		t.Errorf("screenshot file empty/missing: %v", err)
	}

	// click changes the page content
	if _, err = exec2(t, ctx, "click", `{"selector":"#btn"}`); err != nil {
		t.Fatal(err)
	}
	out, _ = exec2(t, ctx, "content", `{"selector":"#out"}`)
	if txt := out.(map[string]any)["text"].(string); strings.TrimSpace(txt) != "geklickt" {
		t.Errorf("after click = %q", txt)
	}

	// type is mirrored via oninput
	if _, err = exec2(t, ctx, "type", `{"selector":"#inp","text":"covey"}`); err != nil {
		t.Fatal(err)
	}
	out, _ = exec2(t, ctx, "content", `{"selector":"#echo"}`)
	if txt := out.(map[string]any)["text"].(string); strings.TrimSpace(txt) != "covey" {
		t.Errorf("after type = %q", txt)
	}

	// :has-text — content grabs the innermost hit by visible text
	out, err = exec2(t, ctx, "content", `{"selector":"div:has-text(\"covey\")"}`)
	if err != nil {
		t.Fatalf("has-text content: %v", err)
	}
	if txt := out.(map[string]any)["text"].(string); strings.TrimSpace(txt) != "covey" {
		t.Errorf("has-text content = %q", txt)
	}

	// :has-text — click finds the button by its label text
	if _, err = exec2(t, ctx, "click", `{"selector":"button:has-text(\"Klick\")"}`); err != nil {
		t.Fatalf("has-text click: %v", err)
	}

	// :has-text without a hit → clear error
	if _, err = exec2(t, ctx, "click", `{"selector":"button:has-text(\"gibtsnicht\")"}`); err == nil {
		t.Error("has-text without a hit: expected an error")
	}

	// screenshot with annotation (highlight + label) yields a valid PNG
	out, err = exec2(t, ctx, "screenshot", `{"to":"annot.png","highlight":"button:has-text(\"Klick\")","label":"Button reagiert nicht"}`)
	if err != nil {
		t.Fatalf("annotated screenshot: %v", err)
	}
	annot := out.(map[string]any)["path"].(string)
	if fi, err := os.Stat(annot); err != nil || fi.Size() == 0 {
		t.Errorf("annotated screenshot empty/missing: %v", err)
	}
	// the overlay must be removed again after the screenshot (#covey-annot gone)
	if _, err = exec2(t, ctx, "content", `{"selector":"#covey-annot"}`); err == nil {
		t.Error("annotation overlay was not removed after the screenshot")
	}

	// highlight without a hit → clear error
	if _, err = exec2(t, ctx, "screenshot", `{"to":"x.png","highlight":"button:has-text(\"gibtsnicht\")"}`); err == nil {
		t.Error("highlight without a hit: expected an error")
	}

	// screenshot without a sandbox workdir → error
	if _, err = exec2(t, context.Background(), "screenshot", `{}`); err == nil {
		t.Error("screenshot without a workdir: expected an error")
	}
	// unknown action
	if _, err = exec2(t, ctx, "kaputt", `{}`); err == nil {
		t.Error("unknown action: expected an error")
	}
}
