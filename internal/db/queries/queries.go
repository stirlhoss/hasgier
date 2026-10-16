package queries

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Idempotency queries

func CheckIdempotencyKey(ctx context.Context, pool *pgxpool.Pool, key string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM webhook_events WHERE idem_key = $1)`,
		key,
	).Scan(&exists)
	return exists, err
}

func InsertIdempotencyKey(ctx context.Context, pool *pgxpool.Pool, key, source string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO webhook_events (idem_key, source, status) VALUES ($1, $2, 'processing') ON CONFLICT (idem_key) DO NOTHING`,
		key, source,
	)
	return err
}

func MarkIdempotencyDone(ctx context.Context, pool *pgxpool.Pool, key string) error {
	_, err := pool.Exec(ctx,
		`UPDATE webhook_events SET status = 'done' WHERE idem_key = $1`,
		key,
	)
	return err
}

// Token queries

type QBToken struct {
	RealmID      string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func GetQBToken(ctx context.Context, pool *pgxpool.Pool, realmID string) (*QBToken, error) {
	var t QBToken
	err := pool.QueryRow(ctx,
		`SELECT realm_id, access_token, refresh_token, expires_at FROM qb_tokens WHERE realm_id = $1 ORDER BY updated_at DESC LIMIT 1`,
		realmID,
	).Scan(&t.RealmID, &t.AccessToken, &t.RefreshToken, &t.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func UpsertQBToken(ctx context.Context, pool *pgxpool.Pool, token *QBToken) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO qb_tokens (realm_id, access_token, refresh_token, expires_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (realm_id) DO UPDATE SET
		   access_token = EXCLUDED.access_token,
		   refresh_token = EXCLUDED.refresh_token,
		   expires_at = EXCLUDED.expires_at,
		   updated_at = now()`,
		token.RealmID, token.AccessToken, token.RefreshToken, token.ExpiresAt,
	)
	return err
}

// Mapping queries

func GetMapping(ctx context.Context, pool *pgxpool.Pool, sourceType, sourceID, targetType string) (string, error) {
	var targetID string
	err := pool.QueryRow(ctx,
		`SELECT target_id FROM entity_mappings WHERE source_type = $1 AND source_id = $2 AND target_type = $3`,
		sourceType, sourceID, targetType,
	).Scan(&targetID)
	return targetID, err
}

// Sync state queries

func UpsertSyncState(ctx context.Context, pool *pgxpool.Pool, wooOrderID int64, status string, qbInvoiceID, qbPaymentID, trackingNumber, errorMessage string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO sync_state (woo_order_id, status, qb_invoice_id, qb_payment_id, tracking_number, error_message)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (woo_order_id) DO UPDATE SET
		   status = EXCLUDED.status,
		   qb_invoice_id = COALESCE(EXCLUDED.qb_invoice_id, sync_state.qb_invoice_id),
		   qb_payment_id = COALESCE(EXCLUDED.qb_payment_id, sync_state.qb_payment_id),
		   tracking_number = COALESCE(EXCLUDED.tracking_number, sync_state.tracking_number),
		   error_message = EXCLUDED.error_message,
		   updated_at = now()`,
		wooOrderID, status, qbInvoiceID, qbPaymentID, trackingNumber, errorMessage,
	)
	return err
}

func GetSyncState(ctx context.Context, pool *pgxpool.Pool, wooOrderID int64) (*SyncState, error) {
	var s SyncState
	err := pool.QueryRow(ctx,
		`SELECT woo_order_id, status, qb_invoice_id, qb_payment_id, tracking_number, error_message
		 FROM sync_state WHERE woo_order_id = $1`,
		wooOrderID,
	).Scan(&s.WooOrderID, &s.Status, &s.QBInvoiceID, &s.QBPaymentID, &s.TrackingNumber, &s.ErrorMessage)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

type SyncState struct {
	WooOrderID    int64
	Status        string
	QBInvoiceID   *string
	QBPaymentID   *string
	TrackingNumber *string
	ErrorMessage  *string
}
