package integration

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// The workspace is an agent's home directory in the browser: readable while
// the agent sleeps, and writable — which is why every path that leaves it has
// to be refused here rather than inside the sandbox.
func TestWorkspaceFilesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("dateien-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/files"

	admin.expect(http.MethodGet, base, nil, http.StatusOK)
	admin.expect(http.MethodGet, base+"/usage", nil, http.StatusOK)

	admin.expect(http.MethodPost, base+"/dir", map[string]string{"path": "notizen"}, http.StatusCreated)
	admin.expect(http.MethodPut, base+"/content",
		map[string]string{"path": "notizen/eins.md", "content": "# Eins\n"}, http.StatusOK)

	got := admin.expect(http.MethodGet, base+"/content?path=notizen/eins.md", nil, http.StatusOK)
	if content, _ := got["content"].(string); content != "# Eins\n" {
		t.Errorf("the file came back as %q", content)
	}

	admin.expect(http.MethodPost, base+"/move",
		map[string]string{"from": "notizen/eins.md", "to": "notizen/zwei.md"}, http.StatusOK)
	admin.expect(http.MethodGet, base+"/content?path=notizen/zwei.md", nil, http.StatusOK)

	// A path that tries to climb out is not refused — it is CLAMPED. Every
	// `..` falls away on the detour through "/", and the write then lands
	// inside the home under the remaining name. That is the deliberate shape:
	// as long as the check was a separate step there was a window between
	// checking and opening, and the agent has a shell in the home and could
	// swap a symlink into it. The guarantee to test is therefore not a 400,
	// it is WHERE the file ends up.
	for _, climb := range []struct{ path, lands string }{
		{"../draussen.md", "draussen.md"},
		{"/etc/passwd", "etc/passwd"},
		{"notizen/../../weg.md", "weg.md"},
	} {
		got := admin.expect(http.MethodPut, base+"/content",
			map[string]string{"path": climb.path, "content": "x"}, http.StatusOK)
		if got["path"] != climb.lands {
			t.Errorf("%q landed at %v, expected %q inside the home", climb.path, got["path"], climb.lands)
		}
	}
	// And reading follows the same clamp, so the two cannot disagree about
	// which file a path means.
	admin.expect(http.MethodGet, base+"/content?path=../draussen.md", nil, http.StatusOK)
	admin.expect(http.MethodGet, base+"/content?path=gibtesnicht.md", nil, http.StatusNotFound)

	admin.expect(http.MethodDelete, base+"?path=notizen/zwei.md", nil, http.StatusOK)
}

// The heartbeat is what wakes an agent on a schedule. Firing one by hand is
// how somebody checks that it does what it says.
func TestHeartbeatsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("takt-agent")
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expectList(http.MethodGet, base+"/heartbeats", nil, http.StatusOK)
	// A heartbeat this agent does not declare cannot be fired — the name comes
	// out of its own HEARTBEAT.md.
	admin.expect(http.MethodPost, base+"/heartbeats/gibtesnicht/fire", nil, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/agents/keine-uuid/heartbeats", nil, http.StatusBadRequest)
}

// The org-wide views: the fleet, the audit trail, the event stream's backlog.
// Each is a read somebody opens when something has gone wrong, so each has to
// answer when nothing has.
func TestOrgWideViewsOverTheAPI(t *testing.T) {
	s := newStack(t)
	s.alsSystemadmin(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	admin.expect(http.MethodGet, "/api/v1/fleet", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/audit", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/audit?limit=5", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/approvals", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/inbox", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/improvements", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/guardrails/events", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/platform/requests", nil, http.StatusOK)
	admin.expect(http.MethodGet, "/api/v1/platform/settings", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/platform/orgs", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/platform/accounts", nil, http.StatusOK)
	admin.expectList(http.MethodGet, "/api/v1/platform/waitlist-codes", nil, http.StatusOK)
}

// Getting files back OUT of the workspace: one at a time, a preview for the
// browser, and a selection as a ZIP. Each is a different content type and a
// different header, and the browser does different things with them.
func TestWorkspaceDownloadsOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("abholung-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/files"

	admin.expect(http.MethodPost, base+"/dir", map[string]string{"path": "ergebnisse"}, http.StatusCreated)
	admin.expect(http.MethodPut, base+"/content",
		map[string]string{"path": "ergebnisse/bericht.md", "content": "# Bericht\n\nFertig.\n"}, http.StatusOK)

	// A download is offered as a file, not rendered: the browser is meant to
	// save it.
	resp := admin.do(http.MethodGet, base+"/download?path=ergebnisse/bericht.md", nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the download answered %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Fertig.") {
		t.Errorf("the file came back as %q", body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "bericht.md") {
		t.Errorf("the download names no file: %q", cd)
	}

	// A preview is the opposite: shown in place. Only the types the browser can
	// render safely are served that way — a list, not a guess, so an uploaded
	// .html can never come back as something the browser executes in the
	// instance's own origin.
	admin.expect(http.MethodPut, base+"/content",
		map[string]string{"path": "ergebnisse/bild.svg", "content": `<svg xmlns="http://www.w3.org/2000/svg"/>`},
		http.StatusOK)
	prev := admin.do(http.MethodGet, base+"/preview?path=ergebnisse/bild.svg", nil)
	prev.Body.Close()
	if prev.StatusCode != http.StatusOK {
		t.Fatalf("the preview answered %d", prev.StatusCode)
	}
	if cd := prev.Header.Get("Content-Disposition"); strings.Contains(cd, "attachment") {
		t.Errorf("the preview is offered as a download: %q", cd)
	}
	// A type that is not on the list is refused rather than served with a
	// guessed content type.
	refused := admin.do(http.MethodGet, base+"/preview?path=ergebnisse/bericht.md", nil)
	refused.Body.Close()
	if refused.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("a type that is not served inline answered %d", refused.StatusCode)
	}

	// A selection as a ZIP: the way a whole result leaves the workspace.
	zip := admin.do(http.MethodGet, base+"/zip?path=ergebnisse", nil)
	zipped, _ := io.ReadAll(zip.Body)
	zip.Body.Close()
	if zip.StatusCode != http.StatusOK {
		t.Fatalf("the ZIP answered %d", zip.StatusCode)
	}
	if len(zipped) < 4 || string(zipped[:2]) != "PK" {
		t.Errorf("what came back is not a ZIP (%d bytes)", len(zipped))
	}

	// A path that is not there is not found — for a download that is a 404 and
	// not an empty file, which the browser would happily save.
	miss := admin.do(http.MethodGet, base+"/download?path=gibtesnicht.md", nil)
	miss.Body.Close()
	if miss.StatusCode != http.StatusNotFound {
		t.Errorf("a missing file answered %d", miss.StatusCode)
	}
}

// Putting files IN: the upload the browser uses for a drag & drop into the
// workspace.
func TestWorkspaceUploadOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("hochlade-agent")
	base := "/api/v1/agents/" + agent.ID.String() + "/files"

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// The directory comes from the query, the file from a part named "file".
	part, err := mw.CreateFormFile("file", "notiz.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("Von aussen hereingelegt.\n"))
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, s.http.URL+base+"/upload?path=eingang", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("the upload answered %d", resp.StatusCode)
	}

	got := admin.expect(http.MethodGet, base+"/content?path=eingang/notiz.txt", nil, http.StatusOK)
	if content, _ := got["content"].(string); !strings.Contains(content, "hereingelegt") {
		t.Errorf("the uploaded file reads %q", content)
	}

	// A request that is not multipart at all is a bad request — the browser
	// sends one, and anything else is a client that guessed.
	plain, _ := http.NewRequest(http.MethodPost, s.http.URL+base+"/upload?path=eingang",
		bytes.NewReader([]byte("kein multipart")))
	plain.Header.Set("Content-Type", "application/json")
	resp2, err := admin.http.Do(plain)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("a request that is not multipart answered %d", resp2.StatusCode)
	}
}
