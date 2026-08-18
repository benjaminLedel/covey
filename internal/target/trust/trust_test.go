package trust

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The third capability that decides whether a plugin can come from the
// catalogue (spec/22): a target system behind a company CA. The plugin cannot
// build the trust store — it does not dial — so the host has to, from the
// credential. The test uses a server signed by a certificate no public
// authority knows, which is exactly the case.
func TestBrokeredCATrustsTheSystemAndNothingElse(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	// The server's own certificate, in the shape an operator stores it as
	// <system>_ca.
	cert := srv.Certificate()
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))

	// Without it: the connection fails, and it has to fail. A client that
	// trusted an unknown certificate would be the man in the middle this whole
	// mechanism exists to prevent.
	plain, err := Client("demo", 5*time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Get(srv.URL); err == nil {
		t.Fatal("a certificate nobody vouches for must not verify")
	}

	// With it: the same request works.
	withCA, err := Client("demo", 5*time.Second, caPEM)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := withCA.Get(srv.URL)
	if err != nil {
		t.Fatalf("the brokered CA has to make the endpoint reachable: %v", err)
	}
	resp.Body.Close()

	// And it replaces the public roots rather than joining them: a client built
	// for one system trusts that system's signer and nobody else. Proven the
	// only way that means anything — with a certificate that is a perfectly
	// valid certificate and simply not this server's.
	stranger, err := Client("demo", 5*time.Second, selfSigned(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stranger.Get(srv.URL); err == nil {
		t.Fatal("a client must not accept a server its brokered CA did not sign")
	}
}

// selfSigned mints a certificate that is valid in every way except the one that
// matters here: nobody in this test's trust store signed it.
func selfSigned(t *testing.T) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "somebody else"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// An unreadable certificate is an error, not a warning. Falling back to the
// system roots would connect to the wrong server successfully and say nothing.
func TestUnreadableCAIsRefused(t *testing.T) {
	_, err := Client("demo", time.Second, "not a certificate")
	if err == nil {
		t.Fatal("a PEM that is not one has to be refused")
	}
	if !strings.Contains(err.Error(), "demo_ca") {
		t.Errorf("the error should name the secret to fix, got %v", err)
	}
}
