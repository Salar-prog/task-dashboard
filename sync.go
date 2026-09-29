package main

import "database/sql"

// MailMessage is one unread mail in the member's Task Intake folder.
// Production fills it from Graph; tests use fakes (see sync_test.go).
type MailMessage struct {
	ID      string // internetMessageId — dedupe key
	Subject string
	From    string
}

// OutlookSource lists unread mail in one folder. The folder id comes from the
// users table cache — implementations must never broaden the scope.
type OutlookSource interface {
	ListUnread(folderID string) ([]MailMessage, error)
}

// readMarker is optionally implemented by OutlookSource backends that can
// mark a message read after import. Checked by assertion so the core poll
// path stays backend-agnostic.
type readMarker interface {
	MarkRead(folderID, messageID string) error
}

// PollOutlookForUser imports one connected member's unread intake mail.
// Unconnected members return (0, nil); unknown emails error loudly.
func PollOutlookForUser(db *sql.DB, src OutlookSource, userEmail string) (int, error) {
	u, err := GetUser(db, userEmail)
	if err != nil {
		return 0, err
	}
	if !u.Connected {
		return 0, nil
	}
	mails, err := src.ListUnread(u.OutlookFolderID)
	if err != nil {
		return 0, err
	}
	imported := 0
	for _, m := range mails {
		title := m.Subject
		if title == "" {
			title = "Untitled mail"
		}
		_, inserted, err := ImportTask(db, "outlook", m.ID, title, m.From, u.Email, "sync:"+u.Email)
		if err != nil {
			return imported, err
		}
		if inserted {
			imported++
			if rm, ok := src.(readMarker); ok {
				_ = rm.MarkRead(u.OutlookFolderID, m.ID)
			}
		}
	}
	return imported, nil
}

// (Teams/Graph polling was removed on the local-auth branch: Graph is OAuth-only.)

// RunPollCycle polls every connected member's mailbox with their own stored
// secret. Failures are logged per user without aborting the cycle.
func RunPollCycle(db *sql.DB, key []byte, mkOutlook func(email, secret string) OutlookSource) (int, error) {
	users, err := ListUsers(db)
	if err != nil {
		return 0, err
	}
	imported := 0
	for _, u := range users {
		if u.Role != "member" || !u.Connected {
			continue
		}
		secret, err := OpenUserSecret(db, key, u.Email)
		if err != nil {
			_ = LogImportError(db, "outlook", u.Email, err)
			continue
		}
		n, err := PollOutlookForUser(db, mkOutlook(u.Email, string(secret)), u.Email)
		if err != nil {
			_ = LogImportError(db, "outlook", u.Email, err)
			continue
		}
		imported += n
	}
	return imported, nil
}
