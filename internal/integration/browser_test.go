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

// findChrome looks for a Chromium/Chrome; if it is missing, the browser
// integration test is skipped (just as the suite skips without a dev DB).
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
	t.Skip("no Chromium/Chrome found — browser integration test skipped")
	return ""
}

func fileExists(p string) bool {
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// TestBrowserPluginScreenshotRecording: end-to-end through the real action
// proxy and a real headless Chrome. The agent navigates, reads content and
// takes a screenshot; what is checked is that every action ends up in the
// recording with ok=true AND that the screenshot lands out-of-band as a
// recording_blob, referenced via payload->>'screenshot'.
func TestBrowserPluginScreenshotRecording(t *testing.T) {
	chrome := findChrome(t)
	t.Setenv("COVEY_BROWSER_CHROME_PATH", chrome)

	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Activation is opt-in — as with every target system.
	admin.expect(http.MethodPatch, "/api/v1/targets/browser", map[string]any{"enabled": true}, http.StatusOK)

	// A simple test page; the real Chrome reaches 127.0.0.1 directly (no egress
	// block applies inside the in-process daemon).
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
	// Recording depth 'full' — otherwise the screenshot would not be stored
	// because of the gating (org floor 'standard', spec/06).
	if err := s.registry.SetRecordingLevel(ctx, agent.ID, "full"); err != nil {
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
	waitFor(t, "browser task done", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// Every action with ok=true in the recording.
	for _, action := range []string{"browser:navigate", "browser:content", "browser:screenshot"} {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
			WHERE agent_id=$1 AND kind='action' AND payload->>'action'=$2 AND (payload->>'ok')::bool`,
			agent.ID, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("action %s missing from the recording (count=%d)", action, n)
		}
	}

	// The screenshot references a blob (bytes NOT inline in the payload).
	var blobID string
	if err := s.pool.QueryRow(ctx, `SELECT payload->>'screenshot' FROM recording_events
		WHERE agent_id=$1 AND kind='action' AND payload->>'action'='browser:screenshot'`,
		agent.ID).Scan(&blobID); err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(blobID); err != nil {
		t.Fatalf("the screenshot reference is not a blob id: %q", blobID)
	}
	// There must be NO base64 image in the JSONB (bytes live out-of-band).
	var hasInline bool
	if err := s.pool.QueryRow(ctx, `SELECT (payload ? 'image_b64') FROM recording_events
		WHERE agent_id=$1 AND kind='action' AND payload->>'action'='browser:screenshot'`,
		agent.ID).Scan(&hasInline); err != nil {
		t.Fatal(err)
	}
	if hasInline {
		t.Error("image_b64 must not be in the recording payload (blob is out-of-band)")
	}

	// The blob exists org-scoped and carries real PNG bytes.
	var size int
	var mime string
	if err := s.pool.QueryRow(ctx, `SELECT length(bytes), mime FROM recording_blobs
		WHERE id=$1 AND org_id=$2`, blobID, s.orgID).Scan(&size, &mime); err != nil {
		t.Fatal(err)
	}
	if size < 100 || mime != "image/png" {
		t.Fatalf("implausible blob: size=%d mime=%q", size, mime)
	}

	// Gating: an agent on the org floor 'standard' gets NO screenshot in the
	// recording — the action is recorded, but the image is discarded.
	plain, err := s.registry.Create(ctx, s.orgID, "researcher-std", "Rechercheur (standard)", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, plain.ID, map[string]string{
		"SOUL.md":   "# Rechercheur standard",
		"ACCESS.md": "- system: browser scope: navigate,content,screenshot,click,type",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	body2 := fmt.Sprintf(`[mock:action browser/navigate {"url":%q}]`+
		` [mock:action browser/screenshot {}]`+
		` [mock:result standard-Lauf]`, srv.URL)
	task2, err := s.backlog.Create(ctx, s.orgID, plain.ID, "Standard-Recherche", body2, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "standard task done", 40*time.Second, func() bool {
		return s.taskState(task2.ID) == backlog.StateDone
	})
	// The screenshot action is recorded, but without a blob reference and without a blob.
	var withRef, blobs int
	if err := s.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE payload ? 'screenshot'),
		(SELECT count(*) FROM recording_blobs WHERE agent_id=$1)
		FROM recording_events
		WHERE agent_id=$1 AND kind='action' AND payload->>'action'='browser:screenshot'`,
		plain.ID).Scan(&withRef, &blobs); err != nil {
		t.Fatal(err)
	}
	if withRef != 0 || blobs != 0 {
		t.Fatalf("gating violated: standard agent has screenshot-ref=%d blobs=%d (expected 0/0)", withRef, blobs)
	}
}
