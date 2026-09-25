package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
)

// A 1x1 PNG.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

// uploadMedia posts one file as the notes' media upload does.
func uploadMedia(t *testing.T, c *apiClient, name string, data []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.base+"/api/v1/me/notes/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func fetch(t *testing.T, c *apiClient, path string) (int, string, []byte) {
	t.Helper()
	resp := c.do(http.MethodGet, path, nil)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), raw
}

// TestNotePicturesBelongToTheirSeat (#344): a picture goes into the media
// store, is served to its owner only, is recognised by its bytes rather than
// its name, and goes with the last note that shows it.
func TestNotePicturesBelongToTheirSeat(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "aud@test.local", "display_name": "Aud", "role": "auditor", "password": "auditor-passwort",
	}, http.StatusCreated)
	aud := login(t, s, "aud@test.local", "auditor-passwort")

	status, up := uploadMedia(t, aud, "tafel.png", tinyPNG)
	if status != http.StatusCreated {
		t.Fatalf("upload: %d %v", status, up)
	}
	id, _ := up["id"].(string)
	if up["ref"] != "covey-media://"+id {
		t.Fatalf("the answer carries the note's reference: %v", up)
	}
	path := "/api/v1/me/notes/media/" + id
	if code, ct, raw := fetch(t, aud, path); code != http.StatusOK || ct != "image/png" || !bytes.Equal(raw, tinyPNG) {
		t.Fatalf("the owner gets the picture back: %d %q %d bytes", code, ct, len(raw))
	}
	if code, _, _ := fetch(t, admin, path); code != http.StatusNotFound {
		t.Fatalf("another seat, even the admin, gets nothing: %d", code)
	}

	// The bytes decide, not the name: text named .png, and SVG, are refused.
	if code, _ := uploadMedia(t, aud, "x.png", []byte("<html><script>alert(1)</script></html>")); code != http.StatusUnsupportedMediaType {
		t.Fatalf("HTML under an image name: %d", code)
	}
	if code, _ := uploadMedia(t, aud, "x.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`)); code != http.StatusUnsupportedMediaType {
		t.Fatalf("SVG: %d", code)
	}
	if code, _ := uploadMedia(t, aud, "big.png", append(append([]byte{}, tinyPNG...), make([]byte, 13<<20)...)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over 12 MB: %d", code)
	}

	// Two notes show it; removing it from one keeps it, deleting the other
	// removes it.
	ref := "![](covey-media://" + id + ")"
	n1 := aud.expect(http.MethodPost, "/api/v1/me/notes", map[string]any{"kind": "text", "body": "Tafel " + ref}, http.StatusCreated)
	n2 := aud.expect(http.MethodPost, "/api/v1/me/notes", map[string]any{"kind": "text", "body": "Kopie " + ref}, http.StatusCreated)
	aud.expect(http.MethodPatch, "/api/v1/me/notes/"+n1["id"].(string), map[string]any{"body": "Tafel, ohne Bild"}, http.StatusOK)
	if code, _, _ := fetch(t, aud, path); code != http.StatusOK {
		t.Fatalf("still shown by the other note: %d", code)
	}
	aud.expect(http.MethodDelete, "/api/v1/me/notes/"+n2["id"].(string), nil, http.StatusNoContent)
	if code, _, _ := fetch(t, aud, path); code != http.StatusNotFound {
		t.Fatalf("no note shows it any more, so it is gone: %d", code)
	}
}
