# Hasgier

A Go server integrating ShipStation, QuickBooks, and WooCommerce for accounting automation.

## Architecture

```
webhook/router.go        # chi router: verify → 202 → river enqueue
workers/
  woo_order.go           # river worker: map WooCommerce order → QB invoice
  shipstation.go         # river worker: update QB with tracking
integrations/
  woocommerce/client.go  # REST client wrapper
  shipstation/client.go  # REST client wrapper
  quickbooks/
    client.go            # REST client wrapper
    oauth.go             # OAuth2 flow + token management
db/
  pool.go                # pgx pool setup
  migrations/            # golang-migrate SQL migrations
  queries/               # hand-written or sqlc-generated queries
main.go                  # pgx pool + river + chi server
```

## Stack

| Need | Package |
|---|---|
| Web framework | `chi` |
| HTTP client | stdlib `net/http` |
| Postgres driver | `pgx` |
| Job queue | `river` (Postgres-backed) |
| Migrations | `golang-migrate` |
| OAuth2 | `golang.org/x/oauth2` |
| JSON | stdlib `encoding/json` |
| Config | stdlib `os.Getenv` |

## File Structure

```
hasgier/
├── go.mod
├── go.sum
├── Dockerfile
├── .env.example
├── cmd/
│   └── hasgier/
│       └── main.go
└── internal/
    ├── config/
    │   └── config.go
    ├── db/
    │   ├── pool.go
    │   ├── migrations/
    │   │   └── 001_initial_schema.up.sql
    │   └── queries/
    │       ├── idempotency.go
    │       ├── tokens.go
    │       ├── mappings.go
    │       └── sync_state.go
    ├── integrations/
    │   ├── woocommerce/
    │   │   └── client.go
    │   ├── shipstation/
    │   │   └── client.go
    │   └── quickbooks/
    │       ├── client.go
    │       └── oauth.go
    ├── workers/
    │   ├── woo_order.go
    │   └── shipstation.go
    └── webhook/
        └── router.go
```

## Database Schema

**`internal/db/migrations/001_initial_schema.up.sql`**:

```sql
-- idempotency: prevent duplicate webhook processing
CREATE TABLE webhook_events (
  id SERIAL PRIMARY KEY,
  idem_key TEXT UNIQUE NOT NULL,
  source TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'processing',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- QuickBooks OAuth tokens
CREATE TABLE qb_tokens (
  id SERIAL PRIMARY KEY,
  realm_id TEXT NOT NULL,
  access_token TEXT NOT NULL,
  refresh_token TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- entity mappings: WooCommerce product/variant → QB item
CREATE TABLE entity_mappings (
  id SERIAL PRIMARY KEY,
  source_type TEXT NOT NULL,
  source_id TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  UNIQUE(source_type, source_id, target_type)
);

-- sync state per order
CREATE TABLE sync_state (
  id SERIAL PRIMARY KEY,
  woo_order_id BIGINT UNIQUE NOT NULL,
  status TEXT NOT NULL DEFAULT 'received',
  qb_invoice_id TEXT,
  qb_payment_id TEXT,
  tracking_number TEXT,
  error_message TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## Config

**`internal/config/config.go`** — reads from env vars:

```go
type Config struct {
    Port                 int
    DatabaseURL          string
    WooURL               string
    WooConsumerKey       string
    WooConsumerSecret    string
    WooWebhookSecret     string
    ShipStationAPIKey    string
    ShipStationAPISecret string
    QBClientID           string
    QBClientSecret       string
    QBRedirectURI        string
    QBEnvironment        string // "sandbox" | "production"
}
```

## API Clients

### `internal/integrations/woocommerce/client.go`

REST wrapper for `https://{store}/wp-json/wc/v3/`. Uses consumer key/secret as query params or Basic Auth.

```go
func (c *Client) GetOrder(ctx context.Context, id int) (*Order, error)
func (c *Client) UpdateOrder(ctx context.Context, id int, data Order) (*Order, error)
func (c *Client) ListOrders(ctx context.Context, params url.Values) ([]Order, error)
```

### `internal/integrations/shipstation/client.go`

REST wrapper for `https://ssapi.shipstation.com/`. Uses Basic Auth.

```go
func (c *Client) GetShipment(ctx context.Context, shipmentID int) (*Shipment, error)
func (c *Client) ListOrders(ctx context.Context, params url.Values) ([]Order, error)
```

### `internal/integrations/quickbooks/oauth.go`

OAuth2 flow using `golang.org/x/oauth2`:
- `GetAuthorizationURL()` — builds the Intuit authorize URL
- `ExchangeCode(code, realmID)` — exchanges authorization code for tokens, stores to DB
- `RefreshToken()` — uses refresh token, updates DB
- `GetValidToken()` — returns current access token, refreshes if expired

### `internal/integrations/quickbooks/client.go`

REST API wrapper for QuickBooks Online:

```go
func (c *Client) CreateInvoice(ctx context.Context, invoice *Invoice) (*Invoice, error)
func (c *Client) CreatePayment(ctx context.Context, payment *Payment) (*Payment, error)
func (c *Client) Query(ctx context.Context, query string) ([]byte, error)
func (c *Client) FindCustomer(ctx context.Context, email string) (*Customer, error)
func (c *Client) CreateCustomer(ctx context.Context, customer *Customer) (*Customer, error)
```

All QB requests include `Authorization: Bearer {token}` and `Accept: application/json`. Token refresh is transparent.

## Webhook Router

**`internal/webhook/router.go`** — chi routes:

```go
func Router(cfg *config.Config, pool *pgxpool.Pool, river *river.Client) *chi.Mux {
    r := chi.NewRouter()

    r.Post("/webhooks/woocommerce", handleWooWebhook(cfg, pool, river))
    r.Post("/webhooks/shipstation", handleSSWebhook(cfg, pool, river))
    r.Get("/oauth/qb/callback", handleQBCallback(cfg, pool))
    r.Get("/oauth/qb/authorize", handleQBAuthorize(cfg))

    return r
}
```

### WooCommerce webhook handler

1. Read raw body bytes
2. Get `X-WC-Webhook-Signature` header
3. Compute HMAC-SHA256 of body with webhook secret
4. Constant-time compare with signature → 401 on mismatch
5. Insert idempotency key → skip if duplicate
6. Return `202 Accepted` immediately
7. `river.Enqueue("woo_order_worker", payload)`

### ShipStation webhook handler

1. Verify Basic Auth header matches configured API credentials
2. Read raw body bytes
3. Decompress with `compress/gzip`
4. Parse JSON, check `X-ShipStation-Event` header
5. Insert idempotency key → skip if duplicate
6. Return `200 OK` immediately
7. `river.Enqueue("shipstation_worker", payload)`

## Background Workers

### `internal/workers/woo_order.go`

Triggers on `order.created` and `order.updated` webhooks:

1. Extract order from webhook payload
2. Find or create QuickBooks customer by email
3. Map WooCommerce line items → QB invoice lines using entity_mappings table
4. Map shipping method → QB shipping item
5. Map payment method → QB payment account
6. POST invoice to QuickBooks
7. If order is paid, POST payment to QuickBooks
8. Upsert sync_state row with status `synced` + qb_invoice_id

### `internal/workers/shipstation.go`

Triggers on `SHIP_NOTIFY` and `ITEM_SHIP_NOTIFY`:

1. Extract shipment details + tracking number
2. Look up sync_state by order number
3. Fetch the QB invoice via qb_invoice_id
4. Update invoice with tracking info (ship date, carrier, tracking #)
5. Update sync_state with tracking_number and status

## Main Application

**`cmd/hasgier/main.go`**:

1. Load config from env
2. Run golang-migrate migrations
3. Start pgx connection pool
4. Register river workers
5. Start chi HTTP server
6. Graceful shutdown on SIGTERM

## Deployment

**`Dockerfile`** — two-stage build:

```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /app
COPY . .
RUN go build -o bin/hasgier ./cmd/hasgier

FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /app/bin/hasgier .
EXPOSE 8080
CMD ["./hasgier"]
```

Fly.io deployment:

```bash
fly launch --build-only
fly pg create --name hasgier-db
fly secrets set ...
fly deploy
```

## Build Order

| Step | Files | Effort | Depends on |
|---|---|---|---|
| 1. Project scaffold | `go mod init`, directory structure | 5 min | nothing |
| 2. DB schema + migrations | migration SQL | 15 min | step 1 |
| 3. Config module | `config.go` | 10 min | step 1 |
| 4. DB pool + queries | `pool.go`, `queries/*.go` | 20 min | steps 1-2 |
| 5. WooCommerce client | `woocommerce/client.go` | 30 min | step 3 |
| 6. ShipStation client | `shipstation/client.go` | 20 min | step 3 |
| 7. QuickBooks client | `quickbooks/*.go` | 60 min | steps 3-4 |
| 8. Webhook router | `webhook/router.go` | 40 min | steps 4-7 |
| 9. BG workers | `workers/*.go` | 30 min | steps 4-7 |
| 10. Main | `main.go` | 15 min | steps 4-9 |
| 11. Dockerfile | `Dockerfile` | 10 min | step 10 |

Total build time: ~4 hours for a working MVP.

## Gotchas

1. **ShipStation gzip**: Use stdlib `compress/gzip` — straightforward, no FFI needed.

2. **QuickBooks token refresh**: Must be atomic — store `expires_at` 5 minutes before actual expiry and refresh early to avoid race conditions between workers.

3. **Webhook body double-read**: For WooCommerce you need the raw bytes (for HMAC) AND the parsed JSON. Read once as bytes, store, then parse.

4. **WooCommerce HMAC comparison**: Use `crypto/hmac` + `crypto/sha256` + `crypto/subtle.ConstantTimeCompare`.

5. **River job queue**: Uses `FOR UPDATE SKIP LOCKED` for job fetching. Tables are auto-created by river migrations.
