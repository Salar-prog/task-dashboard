package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
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

func SignSession(key []byte, email string, exp time.Time) (string, error) {
	if len(key) != 32 {
		return "", errors.New("session key must be 32 bytes")
	}
	payload := email + "|" + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.URLEncoding.EncodeToString([]byte(payload + "|" + hex.EncodeToString(sig))), nil
}

func VerifySession(key []byte, cookie string) (string, error) {
	if len(key) != 32 {
		return "", errors.New("session key must be 32 bytes")
	}
	raw, err := base64.URLEncoding.DecodeString(cookie)
	if err != nil {
		return "", err
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", errors.New("bad session format")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0] + "|" + parts[1]))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(want)) {
		return "", errors.New("bad session signature")
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", errors.New("session expired")
	}
	return parts[0], nil
}

func RoleFor(email string, members map[string]bool) string {
	if members[email] {
		return "member"
	}
	return "senior"
}

func WithUser(key []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email := ""
		if c, err := r.Cookie("session"); err == nil {
			email, _ = VerifySession(key, c.Value)
		}
		r.Header.Set("X-User", email)
		next.ServeHTTP(w, r)
	})
}

func HandleDisconnect(db *sql.DB, key []byte) http.Handler {
	_ = key
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email := r.Header.Get("X-User")
		if email == "" {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		_ = ClearUserToken(db, email)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}
