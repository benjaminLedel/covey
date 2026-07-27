package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// findChrome sucht einen Chromium/Chrome; fehlt er, wird der Browser-
// Integrationstest übersprungen (wie die Suite ohne Dev-DB skippt).
func findChrome(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("COVEY_BROWSER_CHROME_PATH"); p != "" {
		return p
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	if p := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; fileExists(p) {
		return p
	}
	t.Skip("kein Chromium/Chrome gefunden — Browser-Integrationstest übersprungen")
	return ""
}

func fileExists(p string) bool {
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// TestBrowserPluginScreenshotRecording: End-to-End über den echten Action-Proxy
// und einen echten headless Chrome. Der Agent navigiert, liest Inhalt und macht
// einen Screenshot; geprüft wird, dass jede Aktion mit ok=true im Recording
// steht UND der Screenshot out-of-band als recording_blob landet, referenziert
// per payload->>'screenshot'.
func TestBrowserPluginScreenshotRecording(t *testing.T) {
	chrome := findChrome(t)
	t.Setenv("COVEY_BROWSER_CHROME_PATH", chrome)

	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Aktivierung ist opt-in — wie bei jedem Zielsystem.
	admin.expect(http.MethodPatch, "/api/v1/targets/browser", map[string]any{"enabled": true}, http.StatusOK)

	// Eine einfache Testseite; der echte Chrome erreicht 127.0.0.1 direkt
	// (im In-Process-Daemon greift keine Egress-Sperre).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Recherche-Testseite</title><h1 id="h">Belegte Aussage</h1>`))
	}))
	t.Cleanup(srv.Close)

	agent, err := s.registry.Create(ctx, s.orgID, "researcher", "Web-Rechercheur", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Web-Rechercheur",
		"ACCESS.md": "- system: browser scope: navigate,content,screenshot,click,type",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	body := fmt.Sprintf(`[mock:action browser/navigate {"url":%q}]`+
		` [mock:action browser/content {"selector":"#h"}]`+
		` [mock:action browser/screenshot {}]`+
		` [mock:result Recherche erledigt]`, srv.URL)
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas recherchieren", body, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "browser-aufgabe done", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// Jede Aktion mit ok=true im Recording.
	for _, action := range []string{"browser:navigate", "browser:content", "browser:screenshot"} {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
			WHERE agent_id=$1 AND kind='action' AND payload->>'action'=$2 AND (payload->>'ok')::bool`,
			agent.ID, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("aktion %s fehlt im recording (count=%d)", action, n)
		}
	}

	// Der Screenshot referenziert einen Blob (Bytes NICHT inline im Payload).
	var blobID string
	if err := s.pool.QueryRow(ctx, `SELECT payload->>'screenshot' FROM recording_events
		WHERE agent_id=$1 AND kind='action' AND payload->>'action'='browser:screenshot'`,
		agent.ID).Scan(&blobID); err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(blobID); err != nil {
		t.Fatalf("screenshot-referenz ist keine blob-id: %q", blobID)
	}
	// Es darf KEIN Base64-Bild im JSONB stehen (Bytes liegen out-of-band).
	var hasInline bool
	if err := s.pool.QueryRow(ctx, `SELECT (payload ? 'image_b64') FROM recording_events
		WHERE agent_id=$1 AND kind='action' AND payload->>'action'='browser:screenshot'`,
		agent.ID).Scan(&hasInline); err != nil {
		t.Fatal(err)
	}
	if hasInline {
		t.Error("image_b64 darf nicht im recording-payload stehen (Blob out-of-band)")
	}

	// Der Blob existiert org-gescopt und trägt echte PNG-Bytes.
	var size int
	var mime string
	if err := s.pool.QueryRow(ctx, `SELECT length(bytes), mime FROM recording_blobs
		WHERE id=$1 AND org_id=$2`, blobID, s.orgID).Scan(&size, &mime); err != nil {
		t.Fatal(err)
	}
	if size < 100 || mime != "image/png" {
		t.Fatalf("blob unplausibel: size=%d mime=%q", size, mime)
	}
}
