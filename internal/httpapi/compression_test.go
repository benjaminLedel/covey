package httpapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A log page is between 265 kB and 1.4 MB of JSON on a real installation. What
// this has to get right is not the compressing — that is a library — but the
// four cases in which compressing is wrong.

func antwort(t *testing.T, handler http.HandlerFunc, akzeptiert string) *http.Response {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil)
	if akzeptiert != "" {
		r.Header.Set("Accept-Encoding", akzeptiert)
	}
	w := httptest.NewRecorder()
	mitKompression(handler).ServeHTTP(w, r)
	return w.Result()
}

func TestJSONWirdKomprimiertUndBleibtLesbar(t *testing.T) {
	inhalt := strings.Repeat(`{"kind":"runtime","payload":"…"},`, 2000)
	resp := antwort(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(inhalt))
	}, "gzip")

	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("JSON has to be compressed: %q", resp.Header.Get("Content-Encoding"))
	}
	// Vary, or a cache hands the compressed body to a client that cannot read it.
	if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary is missing: %q", resp.Header.Get("Vary"))
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != inhalt {
		t.Error("the decompressed body is not the original")
	}
	// The saving is the entire point of this middleware.
	komprimiert := resp.ContentLength
	if komprimiert > 0 && komprimiert > int64(len(inhalt))/4 {
		t.Errorf("barely compressed: %d of %d bytes", komprimiert, len(inhalt))
	}
}

// The live view is the case where compressing would do damage rather than good:
// a line that waits in a compressor is a line that has not arrived.
func TestEreignisstromBleibtUnkomprimiert(t *testing.T) {
	resp := antwort(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: los\n\n"))
	}, "gzip")

	if resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("a stream must not be compressed: %q", resp.Header.Get("Content-Encoding"))
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "data: los\n\n" {
		t.Errorf("the stream arrived changed: %q", got)
	}
}

func TestOhneAnfrageKeineKompression(t *testing.T) {
	resp := antwort(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(strings.Repeat("x", 4000)))
	}, "")

	if resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("nobody asked for gzip: %q", resp.Header.Get("Content-Encoding"))
	}
}

// A Content-Length left over from the uncompressed body is worse than none:
// the client stops reading early and the answer is silently truncated.
func TestLaengeVerschwindetBeimKomprimieren(t *testing.T) {
	inhalt := strings.Repeat("y", 4000)
	resp := antwort(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4000")
		w.Write([]byte(inhalt))
	}, "gzip")

	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("should have been compressed")
	}
	if l := resp.Header.Get("Content-Length"); l == "4000" {
		t.Error("the length of the uncompressed body is still there — the client would truncate")
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(gz)
	if len(got) != len(inhalt) {
		t.Errorf("the body arrived incomplete: %d of %d", len(got), len(inhalt))
	}
}

// Below the threshold the header and trailer are most of the answer.
func TestKurzeAntwortBleibtWieSieIst(t *testing.T) {
	resp := antwort(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "17")
		w.Write([]byte(`{"ok":true,"n":1}`))
	}, "gzip")

	if resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("17 bytes are not worth a gzip stream: %q", resp.Header.Get("Content-Encoding"))
	}
}

// Images, archives and anything else already compressed would only grow.
func TestBereitsKomprimiertesBleibtUnangetastet(t *testing.T) {
	for _, typ := range []string{"image/png", "application/zip", "application/octet-stream"} {
		resp := antwort(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", typ)
			w.Write([]byte(strings.Repeat("z", 4000)))
		}, "gzip")
		if resp.Header.Get("Content-Encoding") != "" {
			t.Errorf("%s must not be compressed", typ)
		}
	}
}

// A handler that flushes expects what it wrote to be on its way. Through a
// compressor that only holds if Flush reaches all the way down.
func TestFlushKommtDurch(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	mitKompression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(strings.Repeat("a", 2000)))
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("the wrapper has to stay flushable")
			return
		}
		f.Flush()
	})).ServeHTTP(w, r)

	if w.Body.Len() == 0 {
		t.Error("after a flush something has to be on the wire")
	}
}
