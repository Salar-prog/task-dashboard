package main

import (
	"bytes"
	"testing"
)

// Refresh tokens rest encrypted; roundtrip must hold and the wrong key must fail.
func TestTokenSealRoundtrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	sealed, err := SealToken(key, []byte("refresh-token-abc"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("refresh-token-abc")) {
		t.Fatalf("sealed blob must not contain plaintext")
	}
	open, err := OpenToken(key, sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(open) != "refresh-token-abc" {
		t.Fatalf("roundtrip: got %q", open)
	}

	wrong := []byte("ffffffffffffffffffffffffffffffff")
	if _, err := OpenToken(wrong, sealed); err == nil {
		t.Fatalf("wrong key must fail")
	}
	if _, err := SealToken([]byte("short"), []byte("x")); err == nil {
		t.Fatalf("short key must fail")
	}
}
