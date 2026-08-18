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
		return reqlog.Client(system, timeout), nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, fmt.Errorf("%s_ca is not a readable PEM certificate", system)
	}
	return &http.Client{
		Timeout: timeout,
		Transport: reqlog.Transport(system, &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}),
	}, nil
}
