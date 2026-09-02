// Package sealbox is the one AES-256-GCM implementation in this repository.
//
// It exists because a second value needed sealing. The SecretStore
// (internal/secrets/builtin) has sealed an organisation's credentials with the
// master key since the beginning; the instance's own configuration
// (internal/settings) now has to seal the SMTP password the same way, and that
// value belongs to no organisation — the `secrets` table cannot carry it, its
// primary key is (org_id, key) with a foreign key to `organizations`.
//
// Two callers with two copies of the same twenty lines is how a nonce ends up
// reused in one of them. So the primitive sits here once, and what stays with
// each caller is the only part that is really theirs: the associated data —
// what a ciphertext is bound to, and therefore which row it may not be moved
// to.
package sealbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Box seals and opens values under one master key.
type Box struct{ aead cipher.AEAD }

// New expects the master key as 64 hex characters (32 bytes → AES-256).
func New(masterKeyHex string) (*Box, error) {
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("COVEY_MASTER_KEY must be 32 bytes of hex (64 characters)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// GenerateMasterKey creates a new master key for bootstrapping.
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// Seal returns a fresh nonce and the ciphertext. The nonce is random per call
// and never derived from the value or its place — two writes of the same
// password to the same key have to look different.
func (b *Box) Seal(aad []byte, value string) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, b.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, b.aead.Seal(nil, nonce, []byte(value), aad), nil
}

// Open reverses Seal. `what` names the value in the error — a decryption that
// fails is nearly always a master key that changed or a row that moved, and
// both are easier to see with a name attached.
func (b *Box) Open(what string, aad, nonce, ciphertext []byte) (string, error) {
	plain, err := b.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", fmt.Errorf("%s: decryption failed: %w", what, err)
	}
	return string(plain), nil
}
