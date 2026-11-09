// Package netguard keeps the control plane's requests to addresses an
// organisation configured out of the host it runs on (#692).
//
// An organisation admin enters URLs — a target system's base URL, an MCP
// server, a voice provider — and the control plane fetches them: to test a
// connection, to list tools, to check a credential every day. Unchecked, such
// a URL can point at the host's loopback services or at the link-local cloud
// metadata endpoint, and the answer comes back in an error message.
//
// What is refused: loopback, link-local, unspecified and multicast addresses.
// Private ranges stay reachable — a self-hosted Zammad or GitLab in the
// company network is the normal case, not an attack. An operator whose target
// genuinely runs on the same host (a local demo double) names it in
// COVEY_OUTBOUND_ALLOW.
//
// The check happens at dial time, against the address the connection really
// goes to, so neither a redirect nor a DNS answer that changes between a check
// and the request gets around it. It applies to requests whose context is
// marked with Guard; everything else — the sandbox, the platform's own
// fetches — is untouched. The egress proxy applies the same check to every
// connection it makes for a sandbox (Control), with the same exceptions.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

type guardKey struct{}

// Guard marks ctx: requests made with it may not reach a refused address.
func Guard(ctx context.Context) context.Context {
	return context.WithValue(ctx, guardKey{}, true)
}

func guarded(ctx context.Context) bool {
	v, _ := ctx.Value(guardKey{}).(bool)
	return v
}

// ErrRefused is the answer for a destination on the host itself.
var ErrRefused = errors.New("destination refused: loopback, link-local and multicast addresses are not reachable from organisation settings (an operator allows them with COVEY_OUTBOUND_ALLOW)")

var (
	allowMu sync.RWMutex
	allow   []*net.IPNet
)

// SetAllow sets the operator's exceptions: CIDRs or single addresses,
// comma-separated (COVEY_OUTBOUND_ALLOW). Invalid entries are reported.
func SetAllow(spec string) error {
	var nets []*net.IPNet
	var bad []string
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			if ip := net.ParseIP(item); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				item = fmt.Sprintf("%s/%d", item, bits)
			}
		}
		_, n, err := net.ParseCIDR(item)
		if err != nil {
			bad = append(bad, item)
			continue
		}
		nets = append(nets, n)
	}
	allowMu.Lock()
	allow = nets
	allowMu.Unlock()
	if len(bad) > 0 {
		return fmt.Errorf("COVEY_OUTBOUND_ALLOW: not an address or CIDR: %s", strings.Join(bad, ", "))
	}
	return nil
}

// Refused tells whether ip is an address on the host itself or otherwise not
// a destination for an organisation-configured URL.
func Refused(ip net.IP) bool {
	allowMu.RLock()
	defer allowMu.RUnlock()
	for _, n := range allow {
		if n.Contains(ip) {
			return false
		}
	}
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast()
}

// CheckURL resolves the host of raw and refuses it if any address it resolves
// to is refused. For callers that cannot put the guard into the client a
// request goes through — the dial-time check stays the real one.
func CheckURL(ctx context.Context, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return nil // not a URL; the plugin says what is wrong with it
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if Refused(ip) {
			return ErrRefused
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil // unreachable anyway; the request reports it
	}
	for _, a := range addrs {
		if Refused(a.IP) {
			return ErrRefused
		}
	}
	return nil
}

// dialer checks the address it is about to connect to, when the request's
// context carries the guard.
var dialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

func dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if !guarded(ctx) {
		return dialer.DialContext(ctx, network, addr)
	}
	d := *dialer
	d.Control = Control
	return d.DialContext(ctx, network, addr)
}

// Control is a net.Dialer.Control hook that refuses a connection to a refused
// address. It runs after name resolution, on the address the socket is about
// to connect to — the egress proxy dials with it too (#705).
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || Refused(ip) {
		return ErrRefused
	}
	return nil
}

var (
	transportOnce sync.Once
	transport     *http.Transport
)

// Transport is http.DefaultTransport with the dial-time check: unmarked
// requests behave exactly as before. Guarded transports never reuse a
// connection across the mark, because the mark decides at dial time and an
// idle connection was dialled without it — so marked requests do not share
// the pool with unmarked ones.
func Transport() http.RoundTripper {
	transportOnce.Do(func() {
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.DialContext = dialContext
		transport = base
	})
	return splitPool{plain: http.DefaultTransport, guarded: transport}
}

type splitPool struct {
	plain, guarded http.RoundTripper
}

func (s splitPool) RoundTrip(req *http.Request) (*http.Response, error) {
	if guarded(req.Context()) {
		return s.guarded.RoundTrip(req)
	}
	return s.plain.RoundTrip(req)
}
