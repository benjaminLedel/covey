// Package egress ist der Enforcement-Punkt für ausgehenden Netzwerk-Verkehr
// der Sandboxen (spec/06, Designprinzip #7: Guard-Rails zentral und außerhalb
// der Runtime, fail-closed). Der Forward-Proxy identifiziert pro Verbindung den
// anfragenden Agenten (Proxy-Authorization) und lässt nur Verbindungen zu Hosts
// auf DESSEN Allowlist zu — alles andere wird abgewiesen und protokolliert.
//
// Enforcement-Stärke hängt vom Sandbox-Provider ab:
//   - docker + network-Isolation: die Sandbox hat keinen anderen Ausgang, der
//     Proxy ist zwingend — nicht umgehbar.
//   - docker + proxy (kooperativ): via HTTP_PROXY, über direkte IPs umgehbar.
//   - local: keine Netz-Isolation möglich (teilt das Host-Netz).
package egress

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Allowlist entscheidet, welche Ziel-Hosts durchgelassen werden. Fail-closed:
// leere Liste → alles abgewiesen. Muster: exakter Host oder "*.suffix".
type Allowlist struct {
	exact    map[string]bool
	suffixes []string
	roots    []string
}

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

// Resolver validiert die Proxy-Credentials eines Verbindungsversuchs und liefert
// die Allowlist des zugehörigen Agenten. Zusätzlich protokolliert er die
// Entscheidungen (Monitoring).
type Resolver interface {
	// Resolve prüft (agentID, token) aus der Proxy-Authorization. ok=false →
	// die Verbindung wird mit 407 abgewiesen (keine/ungültige Credentials).
	Resolve(ctx context.Context, agentID, token string) (allow *Allowlist, agent uuid.UUID, ok bool)
	// Log hält eine Entscheidung fest (host, erlaubt/blockiert).
	Log(agent uuid.UUID, host, method string, allowed bool)
}

// Proxy ist ein HTTP/HTTPS-Forward-Proxy mit per-Agent-Allowlist.
type Proxy struct {
	resolve Resolver
	log     *slog.Logger
	ln      net.Listener
	srv     *http.Server
}

func New(resolver Resolver, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{resolve: resolver, log: log}
}

// Start bindet den Proxy an addr (z. B. ":0") und bedient Anfragen im Hintergrund.
func (p *Proxy) Start(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("egress-proxy binden: %w", err)
	}
	p.ln = ln
	p.srv = &http.Server{Handler: http.HandlerFunc(p.serve)}
	go func() { _ = p.srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

func (p *Proxy) Close() error {
	if p.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.srv.Shutdown(ctx)
}

// authorize liest die Proxy-Authorization und löst den Agenten + seine Allowlist
// auf. Fehlt/ungültig → (nil, Nil, false) und der Aufrufer antwortet mit 407.
func (p *Proxy) authorize(r *http.Request) (*Allowlist, uuid.UUID, bool) {
	user, pass, ok := parseProxyAuth(r.Header.Get("Proxy-Authorization"))
	if !ok {
		return nil, uuid.Nil, false
	}
	return p.resolve.Resolve(r.Context(), user, pass)
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	allow, agent, ok := p.authorize(r)
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="covey-egress"`)
		http.Error(w, "proxy-authentifizierung erforderlich", http.StatusProxyAuthRequired)
		return
	}
	host := r.Host
	if !allow.Allows(host) {
		p.resolve.Log(agent, host, "CONNECT", false)
		http.Error(w, "egress verweigert (nicht auf allowlist)", http.StatusForbidden)
		return
	}
	dstConn, err := net.DialTimeout("tcp", host, 15*time.Second)
	if err != nil {
		p.resolve.Log(agent, host, "CONNECT", false)
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
	p.resolve.Log(agent, host, "CONNECT", true)
	tunnel(clientConn, dstConn)
}

func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host == "" {
		http.Error(w, "nur Proxy-Anfragen (absolute URL)", http.StatusBadRequest)
		return
	}
	allow, agent, ok := p.authorize(r)
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="covey-egress"`)
		http.Error(w, "proxy-authentifizierung erforderlich", http.StatusProxyAuthRequired)
		return
	}
	if !allow.Allows(r.URL.Host) {
		p.resolve.Log(agent, r.URL.Host, r.Method, false)
		http.Error(w, "egress verweigert (nicht auf allowlist)", http.StatusForbidden)
		return
	}
	p.resolve.Log(agent, r.URL.Host, r.Method, true)
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	stripHopByHop(outReq.Header)
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "upstream fehler", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	stripHopByHop(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// stripHopByHop entfernt Proxy- und Hop-by-Hop-Header vor dem Weiterreichen —
// insbesondere die Proxy-Authorization: das per-Sandbox-Token darf den
// Ziel-Host nie erreichen.
func stripHopByHop(h http.Header) {
	for _, f := range strings.Split(h.Get("Connection"), ",") {
		if f = strings.TrimSpace(f); f != "" {
			h.Del(f)
		}
	}
	for _, k := range []string{
		"Proxy-Authorization", "Proxy-Connection", "Connection",
		"Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		h.Del(k)
	}
}

// parseProxyAuth zerlegt "Basic base64(user:pass)" in user/pass.
func parseProxyAuth(header string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return "", "", false
	}
	user, pass, found := strings.Cut(string(raw), ":")
	if !found {
		return "", "", false
	}
	return user, pass, true
}

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
