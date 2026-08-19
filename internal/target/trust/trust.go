// Package trust builds the HTTP client for a target system whose endpoint is
// not signed by a publicly trusted authority — an internal Kubernetes API
// server, an appliance behind a company CA.
//
// It exists because of who dials. A compiled plugin makes its own connection
// and can build its own trust store; a manifest and a wasm module cannot, and
// the host makes the request for them. So the trust anchor is brokered like the
// token (target.Credential.CA, from the optional secret <system>_ca) and turned
// into a client here, once, for every plugin kind.
//
// The CA REPLACES the system roots rather than joining them. Whoever names a
// certificate for a system is saying who signs it; letting three hundred public
// authorities stay valid beside it would give away most of what was gained.
// There is deliberately no way to skip verification at all: a client that talks
// to a production cluster without checking who answers is a man in the middle
// waiting to happen, and the token it hands over is the one that reads
// everything.
package trust

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"covey/internal/reqlog"
)

// Client returns the client for a system. An empty caPEM gives the ordinary
// one, which is the normal case — anything on the public internet.
//
// An unreadable certificate is an error and not a warning: falling back to the
// system roots would connect successfully to the wrong server and report
// nothing, which is the one outcome nobody wants from a setting called "our
// CA".
func Client(system string, timeout time.Duration, caPEM string) (*http.Client, error) {
	if strings.TrimSpace(caPEM) == "" {
		c := *reqlog.Client(system, timeout)
		c.CheckRedirect = refuseCrossOrigin
		return &c, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, fmt.Errorf("%s_ca is not a readable PEM certificate", system)
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: refuseCrossOrigin,
		Transport: reqlog.Transport(system, &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}),
	}, nil
}

// refuseCrossOrigin stops a brokered request being redirected off the origin an
// organisation pointed the plugin at.
//
// The compiled Kubernetes plugin dialled for itself and refused this, with a
// test to say so. When it became a module the dialling moved here, and the
// property would have moved nowhere — so it is here now, for every plugin the
// host dials for rather than only for that one.
//
// The stricter half is not about the token. Go already strips Authorization on
// a cross-host redirect, so the credential does not travel. What is left is
// that an API server able to answer "look over there" could aim a plugin at a
// host nobody allowed and hand back whatever it found, which is a poor way to
// learn what your cluster is running. An in-origin redirect is ordinary and
// stays allowed.
func refuseCrossOrigin(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	from := via[len(via)-1].URL
	if !sameOrigin(from, req.URL) {
		return fmt.Errorf("refusing redirect from %s to %s — a brokered request stays on the origin it was pointed at",
			origin(from), origin(req.URL))
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(origin(a), origin(b))
}

// origin is host:port with the scheme's default port made explicit, so that
// https://x and https://x:443 are not read as two different places.
func origin(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return host + ":" + port
}
