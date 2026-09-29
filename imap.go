package main

import (
	"fmt"

	"github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
)

// imapEnvelope is one unseen mail. Production fills it from go-imap;
// tests use fakes behind imapConn.
type imapEnvelope struct {
	ID      string // Message-ID header — dedupe key
	Subject string
	From    string
}

// imapConn is the test seam: listing unseen mail in a folder.
type imapConn interface {
	Unseen(folder string) ([]imapEnvelope, error)
	Close() error
}

// imapListUnread maps one folder's unseen mail, always closing the conn.
func imapListUnread(open func() (imapConn, error), folder string) ([]MailMessage, error) {
	c, err := open()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	envs, err := c.Unseen(folder)
	if err != nil {
		return nil, err
	}
	out := make([]MailMessage, 0, len(envs))
	for _, e := range envs {
		out = append(out, MailMessage{ID: e.ID, Subject: e.Subject, From: e.From})
	}
	return out, nil
}

// goIMAPConn is the live go-imap adapter (thin; verified by smoke, not unit).
type goIMAPConn struct {
	c    *imapclient.Client
	user string
	pass string
}

func dialIMAP(host, user, pass string) (imapConn, error) {
	c, err := imapclient.DialTLS(host, nil)
	if err != nil {
		return nil, err
	}
	if err := c.Login(user, pass); err != nil {
		c.Logout()
		return nil, err
	}
	return &goIMAPConn{c: c, user: user, pass: pass}, nil
}

func (g *goIMAPConn) Unseen(folder string) ([]imapEnvelope, error) {
	if _, err := g.c.Select(folder, false); err != nil {
		return nil, fmt.Errorf("select %q: %w", folder, err)
	}
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	uids, err := g.c.Search(criteria)
	if err != nil {
		return nil, err
	}
	if len(uids) == 0 {
		return nil, nil
	}
	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)
	msgs := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- g.c.UidFetch(seqset, []imap.FetchItem{imap.FetchEnvelope}, msgs) }()
	var out []imapEnvelope
	for m := range msgs {
		env := m.Envelope
		if env == nil {
			continue
		}
		from := ""
		if len(env.From) > 0 {
			from = env.From[0].MailboxName + "@" + env.From[0].HostName
		}
		id := env.MessageId
		if id == "" {
			id = fmt.Sprintf("%s/%s", from, env.Subject)
		}
		out = append(out, imapEnvelope{ID: id, Subject: env.Subject, From: from})
	}
	if err := <-done; err != nil {
		return nil, err
	}
	return out, nil
}

func (g *goIMAPConn) Close() error { return g.c.Logout() }

// imapOutlook polls one member's mailbox; folder comes from the caller
// (PollOutlookForUser passes the cached OutlookFolderID).
type imapOutlook struct {
	host, user, secret string
}

func (o imapOutlook) ListUnread(folderID string) ([]MailMessage, error) {
	return imapListUnread(func() (imapConn, error) {
		return dialIMAP(o.host, o.user, o.secret)
	}, folderID)
}
