package sealbox

import (
	"bytes"
	"testing"
)

func TestSealAndOpen(t *testing.T) {
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}

	nonce, ct, err := b.Seal([]byte("system:mail.smtp_password"), "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open("password", []byte("system:mail.smtp_password"), nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hunter2" {
		t.Errorf("read back %q", got)
	}

	// The associated data is what keeps a ciphertext in its row. Opening it
	// under another key's name has to fail, otherwise a value could be moved
	// from one setting to another by an UPDATE.
	if _, err := b.Open("password", []byte("system:mail.from"), nonce, ct); err == nil {
		t.Error("the ciphertext opened under foreign associated data")
	}

	// A second master key does not open it either.
	other, _ := GenerateMasterKey()
	ob, _ := New(other)
	if _, err := ob.Open("password", []byte("system:mail.smtp_password"), nonce, ct); err == nil {
		t.Error("a foreign master key opened the value")
	}
}

// Two seals of the same value have to differ. A nonce reused under one key is
// the one mistake AES-GCM does not forgive.
func TestNonceIsFresh(t *testing.T) {
	key, _ := GenerateMasterKey()
	b, _ := New(key)
	n1, c1, _ := b.Seal([]byte("a"), "same")
	n2, c2, _ := b.Seal([]byte("a"), "same")
	if bytes.Equal(n1, n2) {
		t.Fatal("the same nonce twice")
	}
	if bytes.Equal(c1, c2) {
		t.Error("identical ciphertext for the same value")
	}
}

func TestKeyMustBe32Bytes(t *testing.T) {
	for _, bad := range []string{"", "abcd", "not-hex-at-all"} {
		if _, err := New(bad); err == nil {
			t.Errorf("%q was accepted as a master key", bad)
		}
	}
}
