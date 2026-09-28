package main

import (
	"errors"
	"testing"
)

type fakeOutlook struct {
	folderSeen string
	mails      []MailMessage
	err        error
}

func (f *fakeOutlook) ListUnread(folderID string) ([]MailMessage, error) {
	f.folderSeen = folderID
	if f.err != nil {
		return nil, f.err
	}
	return f.mails, nil
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

type fakeTeams struct {
	msgs []TeamsMessage
	err  error
}

func (f *fakeTeams) ListRecent(teamID, channelID string) ([]TeamsMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.msgs, nil
}

// Only @mention messages become tasks; owner = mentioned member else sender;
// re-poll dedupes on message id.
func TestTeamsPollImportsMentions(t *testing.T) {
	db := openTestDB(t)
	members := map[string]bool{"member1@example.com": true}

	src := &fakeTeams{msgs: []TeamsMessage{
		{ID: "msg-1", Text: "Please handle deploy\nDetails here", From: "boss@example.com", Mentioned: []string{"member1@example.com"}},
		{ID: "msg-2", Text: "FYI no mention", From: "boss@example.com"},
		{ID: "msg-1", Text: "Please handle deploy\nDetails here", From: "boss@example.com", Mentioned: []string{"member1@example.com"}},
		{ID: "msg-3", Text: "Ping outsider", From: "boss@example.com", Mentioned: []string{"stranger@example.com"}},
	}}
	imported, err := PollTeams(db, src, "team-1", "chan-1", members)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if imported != 2 {
		t.Fatalf("want 2 new tasks, got %d", imported)
	}
	var owner, title, assigner string
	if err := db.QueryRow(`SELECT owner, title, assigner FROM tasks WHERE source_ref='msg-1'`).
		Scan(&owner, &title, &assigner); err != nil {
		t.Fatalf("row: %v", err)
	}
	if owner != "member1@example.com" || title != "Please handle deploy" || assigner != "boss@example.com" {
		t.Fatalf("mapping wrong: %q %q %q", owner, title, assigner)
	}
	if err := db.QueryRow(`SELECT owner FROM tasks WHERE source_ref='msg-3'`).Scan(&owner); err != nil {
		t.Fatalf("outsider row: %v", err)
	}
	if owner != "boss@example.com" {
		t.Fatalf("non-member mention falls back to sender, got %q", owner)
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

func TestRunPollCycle(t *testing.T) {
	db := openTestDB(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	if err := UpsertUser(db, User{Email: "member1@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	sealed, _ := SealToken(key, []byte("refresh-xyz"))
	if err := SetUserConnected(db, "member1@example.com", sealed, "folder-1"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertUser(db, User{Email: "member2@example.com", Role: "member"}); err != nil {
		t.Fatal(err)
	}

	outlook := &fakeOutlook{mails: []MailMessage{{ID: "om1", Subject: "S", From: "b@x.com"}}}
	teams := &fakeTeams{msgs: []TeamsMessage{{ID: "tm1", Text: "Hi", From: "b@x.com", Mentioned: []string{"member1@example.com"}}}}
	mkOutlook := func(token string) OutlookSource { return outlook }
	mkTeams := func(token string) (TeamsSource, string, string) { return teams, "t1", "c1" }

	on, tn, err := RunPollCycle(db, key, fakeRefresher{access: "a"}, mkOutlook, mkTeams)
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if on != 1 || tn != 1 {
		t.Fatalf("want 1+1, got %d+%d", on, tn)
	}
}
