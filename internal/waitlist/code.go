// Package waitlist manages the codes with which Covey opens in stages: create
// one, hand it out, redeem it once with a sign-up.
//
// The format follows from the way a code travels. It stands in an e-mail, on a
// conference badge, and is read out over the phone — so it uses Crockford
// Base32, whose whole point is exactly that: the alphabet contains no I, L, O
// or U, and reading maps O to 0 and I/L to 1. The usual typo corrects itself
// instead of producing an "invalid code" that nobody can explain.
package waitlist

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Prefix makes the code recognisable in a mail as belonging to Covey. It is
// not part of the secret and is stripped before anything else — it must not
// take part in normalisation, because its own letters (C, O, V, E, Y) are
// symbols of the alphabet and an O would otherwise turn into a 0.
const Prefix = "COVEY"

// alphabet is Crockford Base32: 32 symbols, 5 bits each.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// symbols is the length of the random part: 10 symbols = 50 bits, about 10^15
// possibilities. Together with the rate limit on the sign-up that carries
// comfortably — the code is a gate, not a secret that protects data.
const symbols = 10

// group is where the hyphens go. Two groups of five read better than one block
// of ten and are easier to dictate.
const group = 5

// NewCode draws a code and returns it in its readable form
// (COVEY-4K7MQ-P2D9X). This is the only moment the plaintext exists: stored is
// only its hash.
func NewCode() (string, error) {
	buf := make([]byte, symbols)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(Prefix)
	for i, v := range buf {
		if i%group == 0 {
			b.WriteByte('-')
		}
		// Modulo on a byte over a 32-symbol alphabet: 256 = 8*32, so every
		// symbol is equally likely — no bias to correct for.
		b.WriteByte(alphabet[int(v)%len(alphabet)])
	}
	return b.String(), nil
}

// Normalize brings an entered code into its canonical form — the one that is
// hashed. Whoever types it in lower case, without hyphens or with spaces gets
// through; whoever mistakes an O for a 0 does too.
//
// The second return value is false when the input cannot be a code at all.
// That check happens before any database access: a wrong length is not a
// question worth asking Postgres.
func Normalize(input string) (string, bool) {
	s := strings.ToUpper(strings.TrimSpace(input))
	s = strings.TrimPrefix(s, Prefix)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '-' || r == ' ' || r == '_':
			continue // separators are decoration
		case r == 'O':
			b.WriteByte('0')
		case r == 'I' || r == 'L':
			b.WriteByte('1')
		case strings.ContainsRune(alphabet, r):
			b.WriteRune(r)
		default:
			return "", false // U, punctuation, anything else: not a code
		}
	}
	out := b.String()
	if len(out) != symbols {
		return "", false
	}
	return out, true
}

// Hash is what the database holds. Same primitive as for the session tokens
// (sha256, hex): a code is a bearer token with a short life, not a password —
// it needs no key derivation, but it has no business lying around in plaintext
// either.
func Hash(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// Format writes a canonical code back into its readable form. Used where a
// freshly created code is shown.
func Format(canonical string) string {
	var b strings.Builder
	b.WriteString(Prefix)
	for i := 0; i < len(canonical); i += group {
		end := min(i+group, len(canonical))
		fmt.Fprintf(&b, "-%s", canonical[i:end])
	}
	return b.String()
}
