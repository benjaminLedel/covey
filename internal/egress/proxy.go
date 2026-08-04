// Package egress is the enforcement point for outbound network traffic from
// the sandboxes (spec/06, design principle #7: guard-rails centrally and
// outside the runtime, fail-closed). The forward proxy identifies the
// requesting agent per connection (Proxy-Authorization) and only permits
// connections to hosts on THAT agent's allowlist — everything else is rejected
// and logged.
//
// How strong the enforcement is depends on the sandbox provider:
//   - docker + network isolation: the sandbox has no other way out, the proxy
//     is mandatory — not bypassable.
//   - docker + proxy (cooperative): via HTTP_PROXY, bypassable through direct IPs.
//   - local: no network isolation possible (shares the host network).
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

// Allowlist decides which target hosts get through. Fail-closed: an empty list
// rejects everything. Patterns: exact host or "*.suffix".
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

// Allows checks a host (with or without port) against the allowlist.
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

// Resolver validates the proxy credentials of a connection attempt and returns
// the allowlist of the corresponding agent. It also records the decisions
// (monitoring).
type Resolver interface {
	// Resolve checks (agentID, token) from the Proxy-Authorization header.
	// ok=false → the connection is rejected with 407 (missing/invalid
	// credentials).
	Resolve(ctx context.Context, agentID, token string) (allow *Allowlist, agent uuid.UUID, ok bool)
	// Log records a decision (host, allowed/blocked).
	Log(agent uuid.UUID, host, method string, allowed bool)
}

// Proxy is an HTTP/HTTPS forward proxy with a per-agent allowlist.
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

// Start binds the proxy to addr (e.g. ":0") and serves requests in the background.
func (p *Proxy) Start(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("bind egress proxy: %w", err)
	}
	p.ln = ln
	// ReadHeaderTimeout here as well: the proxy is reachable from every sandbox,
	// and a sandbox is precisely the place where foreign code runs.
	p.srv = &http.Server{
		Handler:           http.HandlerFunc(p.serve),
		ReadHeaderTimeout: 20 * time.Second,
	}
	go func() { _ = p.srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

func (p *Proxy) Close() error {
	if p.srv == nil {
		return nil
	}
	// Shutting down needs its own context: the caller typically closes the proxy
	// BECAUSE their context has expired.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.srv.Shutdown(ctx)
}

// authorize reads the Proxy-Authorization header and resolves the agent plus
// its allowlist. Missing/invalid → (nil, Nil, false) and the caller answers
// with 407.
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
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	host := r.Host
	if !allow.Allows(host) {
		p.resolve.Log(agent, host, "CONNECT", false)
		http.Error(w, "egress denied (not on allowlist)", http.StatusForbidden)
		return
	}
	dstConn, err := net.DialTimeout("tcp", host, 15*time.Second)
	if err != nil {
		p.resolve.Log(agent, host, "CONNECT", false)
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer dstConn.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
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
		http.Error(w, "proxy requests only (absolute URL)", http.StatusBadRequest)
		return
	}
	allow, agent, ok := p.authorize(r)
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="covey-egress"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if !allow.Allows(r.URL.Host) {
		p.resolve.Log(agent, r.URL.Host, r.Method, false)
		http.Error(w, "egress denied (not on allowlist)", http.StatusForbidden)
		return
	}
	p.resolve.Log(agent, r.URL.Host, r.Method, true)
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	stripHopByHop(outReq.Header)
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
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

// stripHopByHop removes proxy and hop-by-hop headers before forwarding — the
// Proxy-Authorization above all: the per-sandbox token must never reach the
// target host.
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

// parseProxyAuth splits "Basic base64(user:pass)" into user/pass.
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
