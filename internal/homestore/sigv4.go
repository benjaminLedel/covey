package homestore

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AWS Signature Version 4 — the whole of what an S3 client needs in the way of
// cryptography.
//
// Built here rather than pulled in, and the spec measured why: minio-go brings
// 18 indirect modules and 41 compiled foreign packages into a project that has
// 22 modules in total, for replication, ILM, notifications and S3 Select that
// will never be used. What a block store needs is five operations, and because
// blocks are small and immutable the most laborious part of an S3 client falls
// away entirely: multipart upload is never needed.
//
// It is also the kind of thing that is defensible to build. SigV4 is a
// signature recipe over HMAC-SHA256, not a handshake — "use crypto primitives,
// do not invent crypto protocols" is not violated by it. And its failure mode
// is loud and closed: a wrong signature yields a 403, immediately visible, not
// a silently weakened security property.

// Credentials of an S3-compatible endpoint.
type Credentials struct {
	AccessKey string
	SecretKey string
	Region    string
}

const (
	algorithm  = "AWS4-HMAC-SHA256"
	service    = "s3"
	terminator = "aws4_request"
	// unsignedPayload is used where the body is not in memory. Not needed for
	// blocks: their content hash IS the payload hash, which is the one place
	// this design saves a pass over the data.
	unsignedPayload = "UNSIGNED-PAYLOAD"
	emptyPayload    = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// Sign adds the SigV4 headers to a request. payloadHash is the hex SHA-256 of
// the body — for a block that is its own hash, which is why nothing has to be
// read twice.
func Sign(req *http.Request, creds Credentials, payloadHash string, now time.Time) {
	if payloadHash == "" {
		payloadHash = emptyPayload
	}
	stamp := now.UTC().Format("20060102T150405Z")
	day := stamp[:8]

	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if req.Host != "" {
		req.Header.Set("Host", req.Host)
	} else {
		req.Header.Set("Host", req.URL.Host)
	}

	signed, canonicalHeaders := canonicalHeaders(req)
	canonical := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signed,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{day, creds.Region, service, terminator}, "/")
	toSign := strings.Join([]string{
		algorithm,
		stamp,
		scope,
		hexSHA256([]byte(canonical)),
	}, "\n")

	signature := hex.EncodeToString(hmacSHA256(signingKey(creds, day), []byte(toSign)))
	req.Header.Set("Authorization", algorithm+
		" Credential="+creds.AccessKey+"/"+scope+
		", SignedHeaders="+signed+
		", Signature="+signature)
}

// signingKey is the chain that binds a signature to day, region and service —
// which is what keeps a captured signature from being usable elsewhere.
func signingKey(creds Credentials, day string) []byte {
	k := hmacSHA256([]byte("AWS4"+creds.SecretKey), []byte(day))
	k = hmacSHA256(k, []byte(creds.Region))
	k = hmacSHA256(k, []byte(service))
	return hmacSHA256(k, []byte(terminator))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonicalURI is the path, each segment percent-encoded — but the separators
// kept. S3 signs the path as it appears on the wire, so a key with a slash in
// it has to keep it.
func canonicalURI(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	return path
}

// canonicalQuery sorts the parameters and encodes them the way SigV4 wants:
// every character except the unreserved ones, and a space as %20 rather than
// as a plus.
func canonicalQuery(u *url.URL) string {
	values := u.Query()
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := values[k]
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, escape(k)+"="+escape(v))
		}
	}
	return strings.Join(parts, "&")
}

// escape is RFC 3986 percent encoding. net/url does not offer exactly this:
// QueryEscape turns a space into a plus, which SigV4 does not accept.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			b.WriteString("%")
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// canonicalHeaders returns the signed header list and their canonical form.
// Everything that is set is signed: a header the signature does not cover is
// one a proxy in between could change without anyone noticing.
func canonicalHeaders(req *http.Request) (signed, canonical string) {
	names := make([]string, 0, len(req.Header)+1)
	values := map[string]string{}
	for name, vals := range req.Header {
		lower := strings.ToLower(name)
		// Content-Length is not signed: Go sets it itself, and a value that
		// differs between signing and sending is the classic source of a 403
		// nobody can explain.
		if lower == "authorization" || lower == "content-length" || lower == "user-agent" {
			continue
		}
		names = append(names, lower)
		trimmed := make([]string, 0, len(vals))
		for _, v := range vals {
			trimmed = append(trimmed, strings.Join(strings.Fields(v), " "))
		}
		values[lower] = strings.Join(trimmed, ",")
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString(":")
		b.WriteString(values[name])
		b.WriteString("\n")
	}
	return strings.Join(names, ";"), b.String()
}
