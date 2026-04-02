package crypto

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	c, err := New(key)
	if err != nil {
		t.Fatalf("new crypto: %v", err)
	}

	plain := []byte("secret-token")
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(dec) != string(plain) {
		t.Fatalf("got %q want %q", dec, plain)
	}
}
