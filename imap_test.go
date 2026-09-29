package main

import (
	"errors"
	"testing"
)

type fakeIMAPConn struct {
	mails  []imapEnvelope
	err    error
	closed bool
	folder string
}

func (f *fakeIMAPConn) Unseen(folder string) ([]imapEnvelope, error) {
	f.folder = folder
	if f.err != nil {
		return nil, f.err
	}
	return f.mails, nil
}

func (f *fakeIMAPConn) Close() error { f.closed = true; return nil }

// Mapping: Message-ID → dedupe key, subject/from through, folder passed to
// the server, connection always closed, server errors propagate.
func TestIMAPListUnread(t *testing.T) {
	conn := &fakeIMAPConn{mails: []imapEnvelope{
		{ID: "<m1@x>", Subject: "Hello", From: "boss@example.com"},
		{ID: "<m2@x>", Subject: "", From: "p@example.com"},
	}}
	mails, err := imapListUnread(func() (imapConn, error) { return conn, nil }, "Task Intake")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mails) != 2 || mails[0].ID != "<m1@x>" || mails[0].From != "boss@example.com" {
		t.Fatalf("mapping wrong: %+v", mails)
	}
	if conn.folder != "Task Intake" || !conn.closed {
		t.Fatalf("folder/close wrong: %q %v", conn.folder, conn.closed)
	}

	if _, err := imapListUnread(func() (imapConn, error) { return nil, errors.New("dial") }, "F"); err == nil {
		t.Fatalf("dial error must propagate")
	}
	broken := &fakeIMAPConn{err: errors.New("search")}
	if _, err := imapListUnread(func() (imapConn, error) { return broken, nil }, "F"); err == nil {
		t.Fatalf("server error must propagate")
	}
	if !broken.closed {
		t.Fatalf("connection must close even on error")
	}
}

// Sealed secret round-trips per user; empty means never connected.
func TestOpenUserSecret(t *testing.T) {
	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	sealed, _ := SealToken(key, []byte("app-pass-1"))
	if err := UpsertUser(db, User{Email: "member1@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if err := SetUserConnected(db, "member1@example.com", sealed, "Task Intake"); err != nil {
		t.Fatal(err)
	}
	got, err := OpenUserSecret(db, key, "member1@example.com")
	if err != nil || string(got) != "app-pass-1" {
		t.Fatalf("got %q %v", got, err)
	}
	if err := UpsertUser(db, User{Email: "member2@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenUserSecret(db, key, "member2@example.com"); err == nil {
		t.Fatalf("empty secret must error")
	}
}

// Cycle polls each connected member with THEIR opened secret, skips the rest,
// logs failures without aborting.
func TestRunPollCycleSecrets(t *testing.T) {
	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	sealed, _ := SealToken(key, []byte("s3cr3t"))
	if err := UpsertUser(db, User{Email: "member1@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if err := SetUserConnected(db, "member1@example.com", sealed, "Task Intake"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertUser(db, User{Email: "member2@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}

	var seen []string
	mkOutlook := func(email, secret string) OutlookSource {
		seen = append(seen, email+"/"+secret)
		return &fakeOutlook{mails: []MailMessage{{ID: "c1", Subject: "S", From: "b@x.com"}}}
	}
	n, err := RunPollCycle(db, key, mkOutlook)
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 import, got %d", n)
	}
	if len(seen) != 1 || seen[0] != "member1@example.com/s3cr3t" {
		t.Fatalf("factory must get email+secret: %q", seen)
	}
	if c := taskCount(t, db); c != 1 {
		t.Fatalf("unconnected member must be skipped, tasks=%d", c)
	}
}
