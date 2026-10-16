package quickbooks

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"hasgier/internal/db/queries"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	qbSandboxBaseURL    = "https://sandbox-quickbooks.api.intuit.com"
	qbProductionBaseURL = "https://quickbooks.api.intuit.com"
	qbAuthURL           = "https://appcenter.intuit.com/connect/oauth2"
	qbTokenURL          = "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer"
	qbRevokeURL         = "https://developer.api.intuit.com/v2/oauth2/tokens/revoke"
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
	src := c.oauth2Config().TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	return src.Token()
}

func (c *OAuthConfig) BaseURL() string {
	if c.Environment == "production" {
		return qbProductionBaseURL
	}
	return qbSandboxBaseURL
}

// GenerateState creates a cryptographically random state parameter for CSRF protection.
func GenerateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
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

// TokenManager handles token lifecycle: storage, retrieval, and refresh.
type TokenManager struct {
	oauthCfg *OAuthConfig
	pool     *pgxpool.Pool
}

func NewTokenManager(oauthCfg *OAuthConfig, pool *pgxpool.Pool) *TokenManager {
	return &TokenManager{oauthCfg: oauthCfg, pool: pool}
}

// GetValidToken returns a valid access token, refreshing if expired.
// Always stores the latest refresh token as Intuit rotates them every ~24 hours.
func (tm *TokenManager) GetValidToken(ctx context.Context, realmID string) (string, error) {
	stored, err := queries.GetQBToken(ctx, tm.pool, realmID)
	if err != nil {
		return "", fmt.Errorf("get stored token: %w", err)
	}

	// If token is still valid (with 5 min buffer), return it
	if time.Now().Add(5 * time.Minute).Before(stored.ExpiresAt) {
		return stored.AccessToken, nil
	}

	// Refresh the token
	newToken, err := tm.oauthCfg.RefreshToken(ctx, stored.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh token: %w", err)
	}

	// Always store the new refresh token — Intuit rotates it
	tokenData := TokenFromOAuth2(newToken, realmID)
	if err := queries.UpsertQBToken(ctx, tm.pool, &queries.QBToken{
		RealmID:      tokenData.RealmID,
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		ExpiresAt:    tokenData.ExpiresAt,
	}); err != nil {
		return "", fmt.Errorf("store refreshed token: %w", err)
	}

	return newToken.AccessToken, nil
}

// StoreTokens saves tokens after initial OAuth exchange.
func (tm *TokenManager) StoreTokens(ctx context.Context, token *oauth2.Token, realmID string) error {
	tokenData := TokenFromOAuth2(token, realmID)
	return queries.UpsertQBToken(ctx, tm.pool, &queries.QBToken{
		RealmID:      tokenData.RealmID,
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		ExpiresAt:    tokenData.ExpiresAt,
	})
}

// SetStateCookie stores the OAuth state in a secure cookie.
func SetStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "qb_oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600, // 10 minutes
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// GetStateCookie reads the OAuth state from the cookie.
func GetStateCookie(r *http.Request) (string, error) {
	cookie, err := r.Cookie("qb_oauth_state")
	if err != nil {
		return "", fmt.Errorf("missing state cookie: %w", err)
	}
	return cookie.Value, nil
}

// ClearStateCookie removes the OAuth state cookie.
func ClearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "qb_oauth_state",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}
