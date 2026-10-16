package quickbooks

import (
	"context"
	"time"

	"golang.org/x/oauth2"
)

const (
	qbSandboxBaseURL    = "https://sandbox-quickbooks.api.intuit.com"
	qbProductionBaseURL = "https://quickbooks.api.intuit.com"
	qbAuthURL           = "https://appcenter.intuit.com/connect/oauth2"
	qbTokenURL          = "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer"
)

type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Environment  string
}

func (c *OAuthConfig) oauth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURI,
		Endpoint: oauth2.Endpoint{
			AuthURL:  qbAuthURL,
			TokenURL: qbTokenURL,
		},
		Scopes: []string{"com.intuit.quickbooks.accounting"},
	}
}

func (c *OAuthConfig) GetAuthorizationURL(state string) string {
	return c.oauth2Config().AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (c *OAuthConfig) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	return c.oauth2Config().Exchange(ctx, code)
}

func (c *OAuthConfig) RefreshToken(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	return c.oauth2Config().TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
}

func (c *OAuthConfig) BaseURL() string {
	if c.Environment == "production" {
		return qbProductionBaseURL
	}
	return qbSandboxBaseURL
}

// TokenData holds the token info to store in the database.
type TokenData struct {
	RealmID      string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func TokenFromOAuth2(token *oauth2.Token, realmID string) *TokenData {
	return &TokenData{
		RealmID:      realmID,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    token.Expiry,
	}
}
