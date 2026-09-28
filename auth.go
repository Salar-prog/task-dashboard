package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
)

// RequireMember gates writes: GET passes for all (senior read-only view),
// non-GET requires a member email or the fallback token.
// X-User carries the verified Entra login email in prod (auth.go wires the
// JWT/session to it); tests set it directly as the seam.
func RequireMember(members map[string]bool, fallbackToken string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		if members[r.Header.Get("X-User")] {
			next.ServeHTTP(w, r)
			return
		}
		if fallbackToken != "" && r.Header.Get("X-Member-Token") == fallbackToken {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	})
}

// SealToken encrypts a refresh token with TOKEN_KEY (32 bytes) via AES-GCM.
func SealToken(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("token key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// OpenToken decrypts what SealToken produced.
func OpenToken(key, sealed []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("token key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("sealed token too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
}
