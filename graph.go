package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/oauth2"
)

func oauthConfigFromEnv() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     os.Getenv("CLIENT_ID"),
		ClientSecret: os.Getenv("CLIENT_SECRET"),
		RedirectURL:  os.Getenv("REDIRECT_URL"),
		Scopes:       []string{"openid", "profile", "email", "offline_access", "User.Read", "Mail.Read", "ChannelMessage.Read.All", "Team.ReadBasic.All"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://login.microsoftonline.com/" + os.Getenv("TENANT_ID") + "/oauth2/v2.0/authorize",
			TokenURL: "https://login.microsoftonline.com/" + os.Getenv("TENANT_ID") + "/oauth2/v2.0/token",
		},
	}
}

type oauthExchanger struct {
	config *oauth2.Config
}

func (o oauthExchanger) Exchange(code string) (string, string, error) {
	tok, err := o.config.Exchange(context.Background(), code)
	if err != nil {
		return "", "", err
	}
	// Email via Graph /me keeps id_token verification out of scope for v1;
	// TLS plus the Entra audience check at the token endpoint and the /me call authenticate the user.
	client := o.config.Client(context.Background(), tok)
	resp, err := client.Get("https://graph.microsoft.com/v1.0/me?$select=mail,userPrincipalName")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var me struct {
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return "", "", err
	}
	email := me.Mail
	if email == "" {
		email = me.UserPrincipalName
	}
	if email == "" {
		return "", "", errors.New("no email on profile")
	}
	return email, tok.RefreshToken, nil
}

type GraphClient struct {
	BaseURL string
	HTTP    *http.Client
}

func graphBase(g *GraphClient) string {
	if g.BaseURL != "" {
		return g.BaseURL
	}
	return "https://graph.microsoft.com/v1.0"
}

func (g *GraphClient) doGet(accessToken, path string, out any) error {
	httpClient := g.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodGet, graphBase(g)+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("graph %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type graphMail struct {
	InternetMessageID string `json:"internetMessageId"`
	Subject           string `json:"subject"`
	From              struct {
		Email struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"from"`
}

func (g *GraphClient) ListUnreadMail(accessToken, folderID string) ([]MailMessage, error) {
	var v struct {
		Value []graphMail `json:"value"`
	}
	path := "/me/mailFolders/" + url.PathEscape(folderID) + "/messages?$filter=isRead%20eq%20false&$top=20&$select=internetMessageId,subject,from"
	if err := g.doGet(accessToken, path, &v); err != nil {
		return nil, err
	}
	out := make([]MailMessage, 0, len(v.Value))
	for _, m := range v.Value {
		out = append(out, MailMessage{ID: m.InternetMessageID, Subject: m.Subject, From: m.From.Email.Address})
	}
	return out, nil
}

type graphChatMsg struct {
	ID   string `json:"id"`
	Body struct {
		Content string `json:"content"`
	} `json:"body"`
	From struct {
		User struct {
			DisplayName string `json:"displayName"`
		} `json:"user"`
	} `json:"from"`
	Mentions []struct {
		Mentioned struct {
			User struct {
				UserPrincipalName string `json:"userPrincipalName"`
				Mail              string `json:"mail"`
				DisplayName       string `json:"displayName"`
			} `json:"user"`
		} `json:"mentioned"`
	} `json:"mentions"`
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func (g *GraphClient) ListChannelMessages(accessToken, teamID, channelID string) ([]TeamsMessage, error) {
	var v struct {
		Value []graphChatMsg `json:"value"`
	}
	path := "/teams/" + url.PathEscape(teamID) + "/channels/" + url.PathEscape(channelID) +
		"/messages?$top=25&$orderby=createdDateTime%20desc"
	if err := g.doGet(accessToken, path, &v); err != nil {
		return nil, err
	}
	out := make([]TeamsMessage, 0, len(v.Value))
	for _, m := range v.Value {
		tm := TeamsMessage{ID: m.ID, Text: stripTags(m.Body.Content), From: m.From.User.DisplayName}
		for _, mt := range m.Mentions {
			u := mt.Mentioned.User
			email := u.UserPrincipalName
			if email == "" {
				email = u.Mail
			}
			if email == "" {
				email = u.DisplayName
			}
			if email != "" {
				tm.Mentioned = append(tm.Mentioned, email)
			}
		}
		out = append(out, tm)
	}
	return out, nil
}
