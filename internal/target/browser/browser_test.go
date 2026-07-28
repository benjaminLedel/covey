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

	"covey/internal/target"
)

func TestCleanURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/a": "https://example.com/a",
		"example.com":           "https://example.com",
		" http://x.test ":       "http://x.test",
	} {
		got, err := cleanURL(in)
		if err != nil || got != want {
			t.Errorf("cleanURL(%q) = %q, %v — will %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "file:///etc/passwd", "ftp://x", "javascript:alert(1)"} {
		if _, err := cleanURL(bad); err == nil {
			t.Errorf("cleanURL(%q): Fehler erwartet", bad)
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
			t.Errorf("PromptDoc ohne Aktion %q", a)
		}
	}
	if (System{}).VerifyWebhook("s", nil, nil) {
		t.Error("VerifyWebhook muss false liefern")
	}
	if _, err := (System{}).ParseWebhook(nil); err == nil {
		t.Error("ParseWebhook muss einen Fehler liefern")
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
			t.Errorf("parseHasText(%q) = (%q,%q,%v) — will (%q,%q,%v)", c.sel, css, needle, ok, c.css, c.needle, c.ok)
		}
	}
}

// findChromium sucht einen Chromium/Chrome für den End-to-End-Test; fehlt er,
// werden die browser-treibenden Tests übersprungen (wie Integrationstests
// ohne Dev-DB).
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
	t.Skip("kein Chromium gefunden — End-to-End-Browsertest übersprungen")
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

	// content der ganzen Seite
	out, err = exec2(t, ctx, "content", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if txt := out.(map[string]any)["text"].(string); !strings.Contains(txt, "Hallo Welt") {
		t.Errorf("content = %q", txt)
	}

	// content per Selektor
	out, err = exec2(t, ctx, "content", `{"selector":"#head"}`)
	if err != nil {
		t.Fatal(err)
	}
	if txt := out.(map[string]any)["text"].(string); strings.TrimSpace(txt) != "Hallo Welt" {
		t.Errorf("content selector = %q", txt)
	}

	// screenshot in die Sandbox
	out, err = exec2(t, ctx, "screenshot", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	shot := out.(map[string]any)["path"].(string)
	if !strings.HasPrefix(shot, filepath.Join(workdir, "browser")) {
		t.Errorf("screenshot-pfad = %q", shot)
	}
	if fi, err := os.Stat(shot); err != nil || fi.Size() == 0 {
		t.Errorf("screenshot-datei leer/fehlt: %v", err)
	}

	// click ändert den Seiteninhalt
	if _, err = exec2(t, ctx, "click", `{"selector":"#btn"}`); err != nil {
		t.Fatal(err)
	}
	out, _ = exec2(t, ctx, "content", `{"selector":"#out"}`)
	if txt := out.(map[string]any)["text"].(string); strings.TrimSpace(txt) != "geklickt" {
		t.Errorf("nach click = %q", txt)
	}

	// type spiegelt sich per oninput
	if _, err = exec2(t, ctx, "type", `{"selector":"#inp","text":"covey"}`); err != nil {
		t.Fatal(err)
	}
	out, _ = exec2(t, ctx, "content", `{"selector":"#echo"}`)
	if txt := out.(map[string]any)["text"].(string); strings.TrimSpace(txt) != "covey" {
		t.Errorf("nach type = %q", txt)
	}

	// :has-text — content greift den innersten Treffer per sichtbarem Text
	out, err = exec2(t, ctx, "content", `{"selector":"div:has-text(\"covey\")"}`)
	if err != nil {
		t.Fatalf("has-text content: %v", err)
	}
	if txt := out.(map[string]any)["text"].(string); strings.TrimSpace(txt) != "covey" {
		t.Errorf("has-text content = %q", txt)
	}

	// :has-text — click findet den Button über seinen Beschriftungstext
	if _, err = exec2(t, ctx, "click", `{"selector":"button:has-text(\"Klick\")"}`); err != nil {
		t.Fatalf("has-text click: %v", err)
	}

	// :has-text ohne Treffer → klarer Fehler
	if _, err = exec2(t, ctx, "click", `{"selector":"button:has-text(\"gibtsnicht\")"}`); err == nil {
		t.Error("has-text ohne Treffer: Fehler erwartet")
	}

	// screenshot mit Annotation (highlight + label) liefert ein gültiges PNG
	out, err = exec2(t, ctx, "screenshot", `{"to":"annot.png","highlight":"button:has-text(\"Klick\")","label":"Button reagiert nicht"}`)
	if err != nil {
		t.Fatalf("annotierter screenshot: %v", err)
	}
	annot := out.(map[string]any)["path"].(string)
	if fi, err := os.Stat(annot); err != nil || fi.Size() == 0 {
		t.Errorf("annotierter screenshot leer/fehlt: %v", err)
	}
	// Overlay muss nach dem Screenshot wieder entfernt sein (#covey-annot weg)
	if _, err = exec2(t, ctx, "content", `{"selector":"#covey-annot"}`); err == nil {
		t.Error("Annotation-Overlay wurde nach dem Screenshot nicht entfernt")
	}

	// highlight ohne Treffer → klarer Fehler
	if _, err = exec2(t, ctx, "screenshot", `{"to":"x.png","highlight":"button:has-text(\"gibtsnicht\")"}`); err == nil {
		t.Error("highlight ohne Treffer: Fehler erwartet")
	}

	// screenshot ohne Sandbox-Workdir → Fehler
	if _, err = exec2(t, context.Background(), "screenshot", `{}`); err == nil {
		t.Error("screenshot ohne Workdir: Fehler erwartet")
	}
	// unbekannte Aktion
	if _, err = exec2(t, ctx, "kaputt", `{}`); err == nil {
		t.Error("unbekannte Aktion: Fehler erwartet")
	}
}
