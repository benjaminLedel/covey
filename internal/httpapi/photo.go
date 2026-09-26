package httpapi

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // decodes an uploaded PNG
	"io"
	"net/http"
	"strconv"

	"covey/internal/mediastore"
	"covey/internal/org"
)

// A person's profile photo (#377). It lives in the media store like a
// note's picture, owned by the person, and humans.photo_id points at it.
// Colleagues of the same organisation may see it; nobody else finds it.

const (
	// maxPhotoUpload bounds what a client may send; the stored photo is far
	// smaller, see photoSize.
	maxPhotoUpload = 12 << 20
	// photoSize is the edge of the stored square: sharp at twice the largest
	// place a face is shown.
	photoSize = 512
)

// handleSetPhoto stores a new photo for the signed-in seat and removes the
// one it replaces.
func (s *Server) handleSetPhoto(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoUpload+1<<20)
	file, _, err := r.FormFile("file")
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, http.StatusRequestEntityTooLarge, "a photo may be at most 12 MB")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "expected a multipart upload with one field \"file\"")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxPhotoUpload+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "upload unreadable")
		return
	}
	if len(data) > maxPhotoUpload {
		writeErr(w, http.StatusRequestEntityTooLarge, "a photo may be at most 12 MB")
		return
	}
	out, err := squarePhoto(data)
	if err != nil {
		writeErr(w, http.StatusUnsupportedMediaType, "only JPEG and PNG photos")
		return
	}
	id, err := s.media().Put(r.Context(), p.OrgID, p.ID, "image/jpeg", out)
	if err != nil {
		mapErr(w, err)
		return
	}
	old, err := s.Org.SetPhoto(r.Context(), p.OrgID, p.ID, &id)
	if err != nil {
		_ = s.media().Delete(r.Context(), p.ID, id)
		mapErr(w, err)
		return
	}
	if old != nil {
		_ = s.media().Delete(r.Context(), p.ID, *old)
	}
	writeJSON(w, http.StatusOK, map[string]string{"photo_id": id.String()})
}

// handleDeletePhoto brings the monogram back.
func (s *Server) handleDeletePhoto(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	old, err := s.Org.SetPhoto(r.Context(), p.OrgID, p.ID, nil)
	if err != nil {
		mapErr(w, err)
		return
	}
	if old != nil {
		_ = s.media().Delete(r.Context(), p.ID, *old)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHumanPhoto serves a person's photo to the members of their
// organisation. A person elsewhere, or one without a photo, is not found.
func (s *Server) handleHumanPhoto(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if !p.HasOrg() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	h, err := s.Org.GetHuman(r.Context(), p.OrgID, id)
	if errors.Is(err, org.ErrNotFound) || (err == nil && h.PhotoID == nil) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	b, err := s.media().Get(r.Context(), h.ID, *h.PhotoID)
	if errors.Is(err, mediastore.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	etag := `"` + h.PhotoID.String() + `"`
	w.Header().Set("ETag", etag)
	// The address carries no version, so a reader asks again after a while;
	// readers that add ?v=<photo_id> get a new address with every photo.
	if r.URL.Query().Get("v") == h.PhotoID.String() {
		w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=300")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", b.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(b.Data)))
	_, _ = w.Write(b.Data)
}

// squarePhoto decodes a JPEG or PNG, cuts the largest centred square out of
// it, scales that down to photoSize and encodes it afresh as JPEG. Encoding
// afresh is the point as much as the size: nothing the camera wrote into
// the file — where, when, with which device — survives it.
func squarePhoto(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	if side < 16 {
		return nil, errors.New("photo too small")
	}
	x0 := b.Min.X + (b.Dx()-side)/2
	y0 := b.Min.Y + (b.Dy()-side)/2
	n := min(side, photoSize)
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	// Area averaging: every target pixel is the mean of the source pixels
	// it covers — no library, and no aliasing when a 4000 px shot shrinks.
	for ty := range n {
		sy0, sy1 := y0+ty*side/n, y0+(ty+1)*side/n
		for tx := range n {
			sx0, sx1 := x0+tx*side/n, x0+(tx+1)*side/n
			var r, g, bl, a, cnt uint64
			for sy := sy0; sy < max(sy1, sy0+1); sy++ {
				for sx := sx0; sx < max(sx1, sx0+1); sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r, g, bl, a, cnt = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), cnt+1
				}
			}
			// A transparent PNG goes onto white, not black.
			av := a / cnt
			white := 0xffff - av
			dst.Set(tx, ty, color.RGBA64{
				R: uint16(r/cnt + white), G: uint16(g/cnt + white), B: uint16(bl/cnt + white), A: 0xffff,
			})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
