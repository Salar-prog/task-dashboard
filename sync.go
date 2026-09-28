package main

import (
	"database/sql"
	"strings"
)

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
		}
	}
	return imported, nil
}

// TeamsMessage is one channel message. Production fills it from Graph.
type TeamsMessage struct {
	ID        string // message id — dedupe key
	Text      string
	From      string
	Mentioned []string // @mentioned emails; empty = chatter, skipped
}

// TeamsSource lists recent messages in one channel.
type TeamsSource interface {
	ListRecent(teamID, channelID string) ([]TeamsMessage, error)
}

// PollTeams imports @mention messages: owner is the first mentioned member,
// else the sender. Messages without mentions are skipped.
func PollTeams(db *sql.DB, src TeamsSource, teamID, channelID string, members map[string]bool) (int, error) {
	msgs, err := src.ListRecent(teamID, channelID)
	if err != nil {
		return 0, err
	}
	imported := 0
	for _, m := range msgs {
		if len(m.Mentioned) == 0 {
			continue
		}
		owner := m.From
		for _, e := range m.Mentioned {
			if members[e] {
				owner = e
				break
			}
		}
		title := strings.SplitN(m.Text, "\n", 2)[0]
		if len(title) > 120 {
			title = title[:120]
		}
		_, inserted, err := ImportTask(db, "teams", m.ID, title, m.From, owner, "sync:"+m.From)
		if err != nil {
			return imported, err
		}
		if inserted {
			imported++
		}
	}
	return imported, nil
}

func RunPollCycle(db *sql.DB, key []byte, r Refresher, mkOutlook func(string) OutlookSource, mkTeams func(string) (TeamsSource, string, string)) (outlookN, teamsN int, err error) {
	users, err := ListUsers(db)
	if err != nil {
		return 0, 0, err
	}
	members := map[string]bool{}
	var firstToken string
	for _, u := range users {
		if u.Role == "member" {
			members[u.Email] = true
		}
		if !u.Connected || len(u.RefreshTokenEnc) == 0 {
			continue
		}
		access, err := EnsureAccessToken(db, key, r, u.Email)
		if err != nil {
			_ = LogImportError(db, "outlook", u.Email, err)
			continue
		}
		if firstToken == "" {
			firstToken = access
		}
		n, err := PollOutlookForUser(db, mkOutlook(access), u.Email)
		if err != nil {
			_ = LogImportError(db, "outlook", u.Email, err)
			continue
		}
		outlookN += n
	}
	if firstToken == "" {
		return outlookN, 0, nil
	}
	src, teamID, channelID := mkTeams(firstToken)
	n, err := PollTeams(db, src, teamID, channelID, members)
	if err != nil {
		_ = LogImportError(db, "teams", teamID+"/"+channelID, err)
		return outlookN, 0, err
	}
	return outlookN, n, nil
}
