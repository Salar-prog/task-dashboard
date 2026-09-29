package main

import (
	"errors"
	"testing"
)

type fakeOutlook struct {
	folderSeen string
	mails      []MailMessage
	err        error
	marked     []string
}

func (f *fakeOutlook) ListUnread(folderID string) ([]MailMessage, error) {
	f.folderSeen = folderID
	if f.err != nil {
		return nil, f.err
	}
	return f.mails, nil
}

func (f *fakeOutlook) MarkRead(folderID, messageID string) error {
	f.marked = append(f.marked, messageID)
	return nil
}

// Connected member: unread mails become tasks (assigner=sender, owner=mailbox),
// re-poll dedupes on internetMessageId, only the cached folder is read.
func TestOutlookPollImportsUnread(t *testing.T) {
	db := openTestDB(t)
	if err := UpsertUser(db, User{Email: "member1@example.com", DisplayName: "M", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if err := SetUserConnected(db, "member1@example.com", []byte("sealed"), "folder-1"); err != nil {
		t.Fatal(err)
	}

	src := &fakeOutlook{mails: []MailMessage{
		{ID: "mail-1", Subject: "Approve budget", From: "boss@example.com"},
		{ID: "mail-1", Subject: "Approve budget", From: "boss@example.com"},
		{ID: "mail-2", Subject: "", From: "peer@example.com"},
	}}
	imported, err := PollOutlookForUser(db, src, "member1@example.com")
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if imported != 2 {
		t.Fatalf("want 2 new tasks, got %d", imported)
	}
	if src.folderSeen != "folder-1" {
		t.Fatalf("must read only cached folder, read %q", src.folderSeen)
	}
	var assigner, owner, title string
	if err := db.QueryRow(`SELECT assigner, owner, title FROM tasks WHERE source_ref='mail-1'`).
		Scan(&assigner, &owner, &title); err != nil {
		t.Fatalf("row: %v", err)
	}
	if assigner != "boss@example.com" || owner != "member1@example.com" || title != "Approve budget" {
		t.Fatalf("mapping wrong: %q %q %q", assigner, owner, title)
	}
	if err := db.QueryRow(`SELECT title FROM tasks WHERE source_ref='mail-2'`).Scan(&title); err != nil {
		t.Fatalf("empty subject row: %v", err)
	}
	if title == "" {
		t.Fatalf("empty subject needs fallback title")
	}

	// Unconnected member: skipped quietly; unknown user: loud error.
	if err := UpsertUser(db, User{Email: "member2@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	n, err := PollOutlookForUser(db, src, "member2@example.com")
	if err != nil || n != 0 {
		t.Fatalf("unconnected: want 0,nil got %d,%v", n, err)
	}
	if _, err := PollOutlookForUser(db, src, "ghost@example.com"); err == nil {
		t.Fatalf("unknown user must error")
	}
}

// Mark-read: PollOutlookForUser calls MarkRead once per INSERTED mail only —
// duplicates (same internetMessageId) must not be marked again.
func TestOutlookPollMarksReadOnInsertOnly(t *testing.T) {
	db := openTestDB(t)
	if err := UpsertUser(db, User{Email: "member1@example.com", DisplayName: "M", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if err := SetUserConnected(db, "member1@example.com", []byte("sealed"), "folder-1"); err != nil {
		t.Fatal(err)
	}

	src := &fakeOutlook{mails: []MailMessage{
		{ID: "mail-1", Subject: "One", From: "boss@example.com"},
		{ID: "mail-1", Subject: "One", From: "boss@example.com"},
		{ID: "mail-2", Subject: "Two", From: "peer@example.com"},
	}}
	imported, err := PollOutlookForUser(db, src, "member1@example.com")
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if imported != 2 {
		t.Fatalf("want 2 new tasks, got %d", imported)
	}
	if len(src.marked) != 2 || src.marked[0] != "mail-1" || src.marked[1] != "mail-2" {
		t.Fatalf("want MarkRead once per inserted mail [mail-1 mail-2], got %v", src.marked)
	}
}

func TestLogImportError(t *testing.T) {
	db := openTestDB(t)
	if err := LogImportError(db, "outlook", "mail-9", errors.New("boom")); err != nil {
		t.Fatalf("log: %v", err)
	}
	var source, ref, msg string
	if err := db.QueryRow(`SELECT source, ref, err FROM import_errors`).Scan(&source, &ref, &msg); err != nil {
		t.Fatalf("row: %v", err)
	}
	if source != "outlook" || ref != "mail-9" || msg != "boom" {
		t.Fatalf("wrong row %q %q %q", source, ref, msg)
	}
}
