package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

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
