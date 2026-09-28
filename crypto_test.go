package main

import (
	"bytes"
	"testing"
	"time"
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

func TestSessionSignVerify(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	c, err := SignSession(key, "member1@example.com", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	email, err := VerifySession(key, c)
	if err != nil || email != "member1@example.com" {
		t.Fatalf("verify: %q %v", email, err)
	}
	if _, err := VerifySession(key, c+"tampered"); err == nil {
		t.Fatalf("tampered cookie must fail")
	}
	expired, _ := SignSession(key, "m@example.com", time.Now().Add(-time.Minute))
	if _, err := VerifySession(key, expired); err == nil {
		t.Fatalf("expired cookie must fail")
	}
}
