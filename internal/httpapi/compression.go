package httpapi

import (
	"bufio"
	"compress/gzip"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// mitKompression compresses what the interface sends back.
//
// It sits here and not in a reverse proxy because of what the default costs:
// nginx compresses text/html and nothing else unless somebody uncomments a
// gzip_types line. Measured on a real installation, that left a 1.1 MB
// frontend bundle and every API response uncompressed — an agent's log page is
// between 265 kB and 1.4 MB of JSON. Whoever installs Covey from GitHub should
// not have to find a proxy setting to make the product usable, and an
// installation with no proxy at all should not be the slow one.
//
// A response is only compressed when the client asked for it, the content type
// benefits and nothing has encoded it already. Anything streaming
// (text/event-stream) is left alone: the point of a live view is that a line
// arrives when it happens, and a compressor between the two is a place for it
// to wait.
func mitKompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary regardless of what we decide below: the same URL answers
		// differently depending on this header, and a cache that does not know
		// that hands a gzipped body to a client that cannot read it.
		w.Header().Add("Vary", "Accept-Encoding")

		// A WebSocket upgrade is not a response body. It hijacks the
		// connection, and wrapping it would put a compressor in the middle of
		// the daemon protocol.
		if !akzeptiertGzip(r) || strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}

		gw := &gzipWriter{ResponseWriter: w}
		defer gw.schliessen()
		next.ServeHTTP(gw, r)
	})
}

func akzeptiertGzip(r *http.Request) bool {
	for _, teil := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if name, _, _ := strings.Cut(strings.TrimSpace(teil), ";"); strings.EqualFold(name, "gzip") {
			return true
		}
	}
	return false
}

// lohntKompression decides by content type. The list is deliberately an
// allowlist: a new binary format that lands here uncompressed costs bandwidth,
// while one that gets compressed twice costs CPU on every request and grows.
func lohntKompression(contentType string) bool {
	typ, _, _ := strings.Cut(contentType, ";")
	typ = strings.ToLower(strings.TrimSpace(typ))
	switch {
	case typ == "text/event-stream":
		return false
	case strings.HasPrefix(typ, "text/"),
		typ == "application/json",
		typ == "application/javascript",
		typ == "application/xml",
		typ == "application/xhtml+xml",
		typ == "image/svg+xml":
		return true
	}
	return false
}

// kleinstesLohnendes: below this a gzip header and trailer are most of the
// answer. Only applied when the handler stated a length — without one there is
// nothing to decide on, and guessing would mean buffering the response.
const kleinstesLohnendes = 512

var gzipPool = sync.Pool{New: func() any {
	// BestSpeed, not the default: JSON compresses about tenfold either way, and
	// what this has to beat is the network, not another compressor.
	w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
	return w
}}

// gzipWriter decides at WriteHeader — that is the first moment the content type
// is known, and the last one at which the headers can still be changed.
type gzipWriter struct {
	http.ResponseWriter
	gz        *gzip.Writer
	entschied bool
}

func (g *gzipWriter) WriteHeader(status int) {
	if g.entschied {
		g.ResponseWriter.WriteHeader(status)
		return
	}
	g.entschied = true

	h := g.ResponseWriter.Header()
	zuKurz := false
	if l := h.Get("Content-Length"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n < kleinstesLohnendes {
			zuKurz = true
		}
	}
	// 204 and 304 carry no body; a response that is already encoded is not ours
	// to encode again.
	leer := status == http.StatusNoContent || status == http.StatusNotModified
	if !zuKurz && !leer && h.Get("Content-Encoding") == "" && lohntKompression(h.Get("Content-Type")) {
		h.Set("Content-Encoding", "gzip")
		// The length is the one of the uncompressed body and is now wrong.
		// Leaving it there is worse than having none: the client truncates.
		h.Del("Content-Length")
		g.gz = gzipPool.Get().(*gzip.Writer)
		g.gz.Reset(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.entschied {
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// Flush has to reach through, or a streaming response would sit in the
// compressor instead of arriving.
func (g *gzipWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack keeps connection upgrades working through this wrapper. The upgrade
// path above already skips them; this is the second lock on the same door,
// because a missing Hijacker does not fail loudly — it fails as a WebSocket
// that never connects.
func (g *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := g.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the underlying writer cannot hijack")
	}
	return h.Hijack()
}

func (g *gzipWriter) schliessen() {
	if g.gz == nil {
		return
	}
	_ = g.gz.Close()
	gzipPool.Put(g.gz)
	g.gz = nil
}
