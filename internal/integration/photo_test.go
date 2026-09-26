package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"covey/internal/mediastore/pg"
	"covey/internal/org"
)

// putPhoto uploads one's own profile photo.
func putPhoto(t *testing.T, c *apiClient, data []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "me.png")
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPut, c.base+"/api/v1/me/photo", &body)
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

// TestAPersonHasAPhoto (#377): a seat sets its photo, the organisation sees
// it square and re-encoded, a new photo replaces the old one, and removing
// it brings the monogram back. Another organisation finds nothing.
func TestAPersonHasAPhoto(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	created := admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "aud@test.local", "display_name": "Aud", "role": "auditor", "password": "auditor-passwort",
	}, http.StatusCreated)
	audID, _ := created["id"].(string)
	aud := login(t, s, "aud@test.local", "auditor-passwort")

	// A 900x600 landscape: the stored photo is its centred square, 512 px.
	wide := image.NewRGBA(image.Rect(0, 0, 900, 600))
	for y := range 600 {
		for x := range 900 {
			wide.Set(x, y, color.RGBA{R: 200, G: 80, B: 40, A: 255})
		}
	}
	var src bytes.Buffer
	_ = png.Encode(&src, wide)
	status, up := putPhoto(t, aud, src.Bytes())
	if status != http.StatusOK {
		t.Fatalf("upload: %d %v", status, up)
	}
	first, _ := up["photo_id"].(string)

	me := aud.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["PhotoID"] != first {
		t.Fatalf("/auth/me carries the photo: %v", me["PhotoID"])
	}
	code, ct, raw := fetch(t, admin, "/api/v1/humans/"+audID+"/photo")
	if code != http.StatusOK || ct != "image/jpeg" {
		t.Fatalf("a colleague sees the photo: %d %q", code, ct)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width != 512 || cfg.Height != 512 {
		t.Fatalf("stored as a 512 px square JPEG: %v %dx%d", err, cfg.Width, cfg.Height)
	}
	var listed bool
	for _, u := range admin.expectList(http.MethodGet, "/api/v1/users", nil, http.StatusOK) {
		listed = listed || (u["id"] == audID && u["photo_id"] == first)
	}
	if !listed {
		t.Fatal("the person's record carries photo_id")
	}

	if code, _ := putPhoto(t, aud, []byte("<svg onload=alert(1)>")); code != http.StatusUnsupportedMediaType {
		t.Fatalf("not a photo: %d", code)
	}

	// A new photo replaces the old one, which is gone from the store.
	status, up = putPhoto(t, aud, src.Bytes())
	second, _ := up["photo_id"].(string)
	if status != http.StatusOK || second == first {
		t.Fatalf("second upload: %d %v", status, up)
	}
	var left int
	_ = s.pool.QueryRow(context.Background(), `SELECT count(*) FROM media WHERE id=$1`, first).Scan(&left)
	if left != 0 {
		t.Fatal("the replaced photo stays behind in the media store")
	}

	// Another organisation's person is not found, photo or not.
	ctx := context.Background()
	other := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1,'Nachbar-AG')`, other); err != nil {
		t.Fatal(err)
	}
	orgs := org.NewStore(s.pool)
	h, err := orgs.CreateHuman(ctx, other, "nb@example.org", "Nachbar", "auditor", "x", org.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	mid, err := pg.New(s.pool).Put(ctx, other, h.ID, "image/jpeg", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orgs.SetPhoto(ctx, other, h.ID, &mid); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := fetch(t, admin, "/api/v1/humans/"+h.ID.String()+"/photo"); code != http.StatusNotFound {
		t.Fatalf("a person of another organisation: %d", code)
	}

	aud.expect(http.MethodDelete, "/api/v1/me/photo", nil, http.StatusNoContent)
	if code, _, _ := fetch(t, admin, "/api/v1/humans/"+audID+"/photo"); code != http.StatusNotFound {
		t.Fatalf("after removal the monogram is back: %d", code)
	}
}
