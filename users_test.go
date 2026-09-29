package main

import (
	"database/sql"
	"errors"
	"testing"
)

// Member connects Outlook once: token + folder cached, connected flips true.
// Unknown email lookup must error (so the poller skips loudly, not silently).
func TestUserUpsertConnect(t *testing.T) {
	db := openTestDB(t)

	if err := UpsertUser(db, User{Email: "member1@example.com", DisplayName: "M One", Role: "member"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	u, err := GetUser(db, "member1@example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if u.Connected {
		t.Fatalf("fresh user must not be connected")
	}

	sealed := []byte("sealed-refresh-token")
	if err := SetUserConnected(db, "member1@example.com", sealed, "folder-1"); err != nil {
		t.Fatalf("connect: %v", err)
	}
	u, err = GetUser(db, "member1@example.com")
	if err != nil {
		t.Fatalf("get after connect: %v", err)
	}
	if !u.Connected || u.OutlookFolderID != "folder-1" || string(u.SecretEnc) != string(sealed) {
		t.Fatalf("connected state wrong: %+v", u)
	}

	if _, err := GetUser(db, "ghost@example.com"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown user: want sql.ErrNoRows, got %v", err)
	}
}
