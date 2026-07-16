// Package egress ist der Enforcement-Punkt für ausgehenden Netzwerk-Verkehr
// der Sandboxen (spec/06, Designprinzip #7: Guard-Rails zentral und außerhalb
// der Runtime, fail-closed). Der Forward-Proxy lässt nur Verbindungen zu Hosts
// auf einer Allowlist zu — alles andere wird abgewiesen.
//
// Enforcement-Stärke: Der Proxy ist der Durchsetzungs-Mechanismus. Ob eine
// Sandbox ihn zwingend benutzen MUSS, hängt vom Sandbox-Provider ab:
//   - docker: Container bekommt HTTP_PROXY/HTTPS_PROXY (kooperativ — ein
//     naiver/versehentlicher Ausleit-Versuch scheitert, ein bewusster über
//     direkte IPs kann den Proxy heute noch umgehen).
//   - local: keine Netz-Isolation möglich (teilt das Host-Netz).
//
// Die harte, manipulationssichere Bindung (internes Docker-Netz ohne Internet,
// Proxy als einziger Ausgang) ist der dokumentierte Folgeschritt — siehe
// docs/betrieb-zammad.md und spec/06.
package egress

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Allowlist entscheidet, welche Ziel-Hosts der Proxy durchlässt. Fail-closed:
// ist die Liste leer, wird alles abgewiesen. Muster:
//   - exakter Host:            "api.anthropic.com"
//   - Wildcard-Subdomain:      "*.example.com" (matcht auch "example.com")
type Allowlist struct {
	exact    map[string]bool
	suffixes []string // ".example.com" für *.example.com
	roots    []string // "example.com" für *.example.com (Apex mit erlaubt)
}

// NewAllowlist baut die Allowlist aus Host-Mustern. Einträge werden getrimmt,
// kleingeschrieben und (falls vorhanden) um den Port bereinigt.
func NewAllowlist(patterns []string) *Allowlist {
	a := &Allowlist{exact: map[string]bool{}}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if h, _, err := net.SplitHostPort(p); err == nil {
			p = h
		}
		if root, ok := strings.CutPrefix(p, "*."); ok {
			a.suffixes = append(a.suffixes, "."+root)
			a.roots = append(a.roots, root)
			continue
		}
		a.exact[p] = true
	}
	return a
}

// Allows prüft einen Host (mit oder ohne Port) gegen die Allowlist.
func (a *Allowlist) Allows(hostPort string) bool {
	host := strings.ToLower(strings.TrimSpace(hostPort))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		return false
	}
	if a.exact[host] {
		return true
	}
	for _, root := range a.roots {
		if host == root {
			return true
		}
	}
	for _, suf := range a.suffixes {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

// Empty meldet, ob die Allowlist keinerlei Muster enthält (dann blockt der
// Proxy fail-closed jeden Verkehr).
func (a *Allowlist) Empty() bool {
	return len(a.exact) == 0 && len(a.suffixes) == 0
}

// Proxy ist ein HTTP/HTTPS-Forward-Proxy mit Host-Allowlist.
type Proxy struct {
	allow *Allowlist
	log   *slog.Logger

	ln   net.Listener
	srv  *http.Server
	once sync.Once
}

// New erstellt den Proxy mit einer Allowlist. log darf nil sein.
func New(allow *Allowlist, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{allow: allow, log: log}
}

// Start bindet den Proxy an addr (z. B. ":0" für einen freien Port) und
// bedient Anfragen im Hintergrund. Gibt die tatsächliche Listen-Adresse zurück.
func (p *Proxy) Start(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("egress-proxy binden: %w", err)
	}
	p.ln = ln
	p.srv = &http.Server{
		Handler:      http.HandlerFunc(p.serve),
		ReadTimeout:  0, // Tunnel laufen lange (SSE/Streaming) — kein Read-Timeout
		WriteTimeout: 0,
	}
	go func() { _ = p.srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

// Close fährt den Proxy herunter.
func (p *Proxy) Close() error {
	if p.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.srv.Shutdown(ctx)
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleConnect bedient HTTPS-Tunnel (CONNECT host:port). Nur erlaubte Hosts
// werden getunnelt; alles andere → 403.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := r.Host // "host:port"
	if !p.allow.Allows(host) {
		p.deny(host, "CONNECT")
		http.Error(w, "egress verweigert (nicht auf allowlist)", http.StatusForbidden)
		return
	}
	dstConn, err := net.DialTimeout("tcp", host, 15*time.Second)
	if err != nil {
		http.Error(w, "upstream nicht erreichbar", http.StatusBadGateway)
		return
	}
	defer dstConn.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack nicht unterstützt", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	tunnel(clientConn, dstConn)
}

// handleHTTP bedient Klartext-HTTP-Proxy-Anfragen (absolute URL).
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host == "" {
		http.Error(w, "nur Proxy-Anfragen (absolute URL)", http.StatusBadRequest)
		return
	}
	if !p.allow.Allows(r.URL.Host) {
		p.deny(r.URL.Host, r.Method)
		http.Error(w, "egress verweigert (nicht auf allowlist)", http.StatusForbidden)
		return
	}
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "upstream fehler", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *Proxy) deny(host, method string) {
	p.log.Warn("egress blockiert", "host", host, "method", method)
}

// tunnel kopiert bidirektional zwischen Client und Upstream, bis eine Seite
// schließt.
func tunnel(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
}
