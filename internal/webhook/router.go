package webhook

import (
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"hasgier/internal/config"
	"hasgier/internal/db/queries"
	"hasgier/internal/integrations/quickbooks"
	"hasgier/internal/workers"
)

type Context struct {
	Config      *config.Config
	Pool        *pgxpool.Pool
	River       *river.Client[pgx.Tx]
	QBOAuth     *quickbooks.OAuthConfig
	QBTokenMgr  *quickbooks.TokenManager
}

func Router(ctx *Context) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Post("/webhooks/woocommerce", ctx.handleWooWebhook)
	r.Post("/webhooks/shipstation", ctx.handleSSWebhook)
	r.Get("/oauth/qb/authorize", ctx.handleQBAuthorize)
	r.Get("/oauth/qb/callback", ctx.handleQBCallback)

	return r
}

// handleWooWebhook handles WooCommerce webhook events.
// Verifies HMAC-SHA256 signature, checks idempotency, enqueues job.
func (c *Context) handleWooWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get("X-WC-Webhook-Signature")
	if signature == "" {
		http.Error(w, "missing signature", http.StatusUnauthorized)
		return
	}

	mac := hmac.New(sha256.New, []byte(c.Config.WooWebhookSecret))
	mac.Write(body)
	expected := fmt.Sprintf("%x", mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) != 1 {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	topic := r.Header.Get("X-WC-Webhook-Topic")
	idemKey := fmt.Sprintf("woo_%s_%s", topic, r.Header.Get("X-WC-Webhook-Delivery-ID"))

	exists, err := queries.CheckIdempotencyKey(r.Context(), c.Pool, idemKey)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	if exists {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if err := queries.InsertIdempotencyKey(r.Context(), c.Pool, idemKey, "woocommerce"); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	payload := map[string]interface{}{
		"topic": topic,
		"body":  json.RawMessage(body),
	}
	_, err = c.River.Insert(r.Context(), workers.WooOrderJob{Payload: payload}, nil)
	if err != nil {
		http.Error(w, "failed to enqueue job", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// handleSSWebhook handles ShipStation webhook events.
// Verifies Basic Auth, decompresses gzip, checks idempotency, enqueues job.
func (c *Context) handleSSWebhook(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok || username != c.Config.ShipStationAPIKey || password != c.Config.ShipStationAPISecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	compressedBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var body []byte
	if r.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(compressedBody))
		if err != nil {
			http.Error(w, "failed to decompress", http.StatusBadRequest)
			return
		}
		body, err = io.ReadAll(reader)
		reader.Close()
		if err != nil {
			http.Error(w, "failed to decompress", http.StatusBadRequest)
			return
		}
	} else {
		body = compressedBody
	}

	event := r.Header.Get("X-ShipStation-Event")
	idemKey := fmt.Sprintf("ss_%s", event)

	exists, err := queries.CheckIdempotencyKey(r.Context(), c.Pool, idemKey)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	if exists {
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := queries.InsertIdempotencyKey(r.Context(), c.Pool, idemKey, "shipstation"); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	payload := map[string]interface{}{
		"event": event,
		"body":  json.RawMessage(body),
	}
	_, err = c.River.Insert(r.Context(), workers.ShipStationJob{Payload: payload}, nil)
	if err != nil {
		http.Error(w, "failed to enqueue job", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleQBAuthorize redirects the user to Intuit's OAuth2 authorization page.
func (c *Context) handleQBAuthorize(w http.ResponseWriter, r *http.Request) {
	state, err := quickbooks.GenerateState()
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}

	quickbooks.SetStateCookie(w, state)

	authURL := c.QBOAuth.GetAuthorizationURL(state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleQBCallback handles the OAuth2 callback from Intuit.
// Exchanges the authorization code for tokens and stores them.
func (c *Context) handleQBCallback(w http.ResponseWriter, r *http.Request) {
	// Verify state for CSRF protection
	stateCookie, err := quickbooks.GetStateCookie(r)
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	quickbooks.ClearStateCookie(w)

	stateParam := r.URL.Query().Get("state")
	if stateParam == "" || stateParam != stateCookie {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	realmID := r.URL.Query().Get("realmId")
	if code == "" || realmID == "" {
		http.Error(w, "missing code or realmId", http.StatusBadRequest)
		return
	}

	// Exchange authorization code for tokens
	token, err := c.QBOAuth.ExchangeCode(r.Context(), code)
	if err != nil {
		http.Error(w, fmt.Sprintf("token exchange failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Store tokens in database
	if err := c.QBTokenMgr.StoreTokens(r.Context(), token, realmID); err != nil {
		http.Error(w, fmt.Sprintf("failed to store tokens: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":   "connected",
		"realm_id": realmID,
	})
}
