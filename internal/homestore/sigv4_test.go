package homestore

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// SigV4 is the one piece of this store that is built rather than pulled in, so
// it is checked against a signature somebody else computed: the documented AWS
// example "GET Object" from the Signature Version 4 test suite. If our
// implementation reproduces it byte for byte, the canonical request, the
// signing key chain and the header handling are all right — and if it does not,
// the fault is here and not in the object store that answers 403.
func TestSignMatchesTheDocumentedAWSExample(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")

	Sign(req, Credentials{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:    "us-east-1",
	}, emptyPayload, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))

	const wantSignature = "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	auth := req.Header.Get("Authorization")
	if !strings.Contains(auth, "Signature="+wantSignature) {
		t.Errorf("signature does not match the AWS example:\n got %s\nwant …Signature=%s", auth, wantSignature)
	}
	if !strings.Contains(auth, "Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request") {
		t.Errorf("scope is wrong: %s", auth)
	}
	if !strings.Contains(auth, "SignedHeaders=host;range;x-amz-content-sha256;x-amz-date") {
		t.Errorf("signed headers are wrong: %s", auth)
	}
}

// A block's hash IS the SHA-256 of its content, so it is exactly what
// x-amz-content-sha256 wants. If that ever drifted apart, every upload would
// come back 403 with a message about a signature mismatch — and the reason
// would be nowhere near it.
func TestPayloadHashTravelsAsTheContentHash(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "https://example.com/bucket/key", nil)
	hash := Hash([]byte("block content"))
	Sign(req, Credentials{AccessKey: "a", SecretKey: "b", Region: "eu"}, hash, time.Now())
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != hash {
		t.Errorf("content hash %q, expected %q", got, hash)
	}
}

// Signing has to be deterministic for a given moment — otherwise a retry would
// carry a different signature than the attempt it repeats, and a debug session
// would start with "it depends".
func TestSignIsDeterministic(t *testing.T) {
	at := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	sign := func() string {
		req, _ := http.NewRequest(http.MethodGet, "https://example.com/bucket/a/b?list-type=2&prefix=x%2Fy", nil)
		Sign(req, Credentials{AccessKey: "key", SecretKey: "secret", Region: "eu-central-1"}, emptyPayload, at)
		return req.Header.Get("Authorization")
	}
	if sign() != sign() {
		t.Error("two signatures of the same request differ")
	}
}

// The query string is signed in SigV4's own encoding, not in Go's: a space has
// to be %20 and never a plus. A store that encoded it differently would answer
// 403 on exactly the requests that carry a delimiter — which is the listing,
// and therefore the retention.
func TestCanonicalQueryUsesRFC3986(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/b?prefix=a+b&list-type=2", nil)
	got := canonicalQuery(req.URL)
	if strings.Contains(got, "+") {
		t.Errorf("a plus must not survive into the canonical query: %q", got)
	}
	// Sorted by name, and every value encoded.
	if got != "list-type=2&prefix=a%20b" {
		t.Errorf("canonical query %q", got)
	}
}
