// Package webhooksig verifies the signature of an inbound webhook — the one
// step of webhook handling that stays with the HOST no matter who wrote the
// plugin.
//
// The reason is the same for both data-driven plugin kinds. Checking an HMAC
// needs the shared secret, and a manifest or a wasm module must never see a
// credential: a plugin that got the secret in order to verify with it could
// also send it somewhere. So the plugin says only WHICH algorithm and WHICH
// header, and the host does the checking. What reaches the plugin afterwards is
// a payload that has already been proven to come from the target system.
package webhooksig

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"net/http"
	"strings"
)

// DefaultHeader is where a signature sits unless the plugin names another one.
const DefaultHeader = "X-Hub-Signature"

// Algorithms a plugin may declare. An empty algorithm means "no verification"
// — legitimate for a target system that has none, and the caller has to decide
// whether it wants to accept that.
const (
	HMACSHA1   = "hmac-sha1"
	HMACSHA256 = "hmac-sha256"
)

// Known reports whether algo is one this package can check. Used at parse and
// install time so an unknown algorithm is refused before it silently lets
// everything through at runtime.
func Known(algo string) bool {
	switch algo {
	case "", HMACSHA1, HMACSHA256:
		return true
	}
	return false
}

// Verify checks the signature of body against secret. An empty algorithm or an
// empty secret passes — the target system has no signature, or the operator has
// configured none; that decision is not made here.
func Verify(algo, headerName, secret string, body []byte, header http.Header) bool {
	if algo == "" || secret == "" {
		return true
	}
	if headerName == "" {
		headerName = DefaultHeader
	}
	var prefix string
	var h hash.Hash
	switch algo {
	case HMACSHA1:
		prefix, h = "sha1=", hmac.New(sha1.New, []byte(secret))
	case HMACSHA256:
		prefix, h = "sha256=", hmac.New(sha256.New, []byte(secret))
	default:
		// An algorithm nobody validated is not a reason to wave the request
		// through: fail closed.
		return false
	}
	h.Write(body)
	got, ok := strings.CutPrefix(header.Get(headerName), prefix)
	if !ok {
		return false
	}
	return hmac.Equal([]byte(hex.EncodeToString(h.Sum(nil))), []byte(got))
}
