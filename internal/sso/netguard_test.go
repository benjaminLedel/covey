package sso

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"

	"covey/internal/netguard"
)

// TestIssuerDiscoveryRefusesHostDestinations pins #692 for single sign-on: the
// issuer is a URL an organisation admin enters, and the control plane fetches
// its discovery document. A destination on the host itself is refused at dial
// time, as for every other organisation-configured URL.
func TestIssuerDiscoveryRefusesHostDestinations(t *testing.T) {
	if err := netguard.SetAllow(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = netguard.SetAllow("") })

	var hits atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(host.Close)

	s := &Service{}
	_, err := oidc.NewProvider(s.ctx(context.Background()), host.URL)
	if !errors.Is(err, netguard.ErrRefused) {
		t.Fatalf("a loopback issuer must be refused, got %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the issuer on loopback was reached %d time(s)", n)
	}
}
