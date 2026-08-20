# Hasgier

A Gleam server integrating ShipStation, QuickBooks, and WooCommerce for accounting automation.

## Architecture

```
webhook_handler.gleam      # Wisp handler: verify → 202 → bg_jobs.enqueue
workers/
  woo_order_worker.gleam    # bg_jobs worker: map WooCommerce order → QB invoice
  shipstation_worker.gleam  # bg_jobs worker: update QB with tracking
integrations/
  woocommerce.gleam         # REST client wrapper
  shipstation.gleam         # REST client wrapper
  quickbooks.gleam          # OAuth2 + REST client wrapper
db/
  schema/                   # squirrel-generated query functions
  migrations/               # cigogne SQL migrations
main.gleam                  # Supervisor: pog pool + bg_jobs + wisp server
```

Supervision tree: `application_supervisor` starts `pog` pool, `bg_jobs` queues, and `wisp` HTTP server. If any crash, OTP restarts them. No single point of failure.

## Dependencies

```toml
wisp = "~> 2.2"
gleam_httpc = "~> 5.0"
gleam_http = "~> 4.3"
pog = "~> 4.1"
bg_jobs = "~> 0.5"
flwr_oauth2 = "~> 3.0"
gleam_crypto = "~> 1.6"
cigogne = "~> 5"
envoy = "~> 1"
gleam_json = "~> 2"
gleam_erlang = "~> 1"
gleam_otp = "~> 1"
gleam_stdlib = "~> 0.52"
squirrel = "~> 4"              # dev dependency
```

## File Structure

```
hasgier/
├── gleam.toml
├── manifest.toml
├── Dockerfile
├── .env.example
├── priv/
│   ├── migrations/
│   │   └── 001_initial_schema.sql
│   └── cigogne.toml
└── src/
    ├── hasgier.gleam                # supervisor + main
    ├── config.gleam                 # env var reading
    ├── sql/                         # squirrel .sql files
    │   ├── create_mapping.sql
    │   ├── update_sync_state.sql
    │   ├── find_idem_key.sql
    │   ├── insert_idem_key.sql
    │   ├── upsert_token.sql
    │   └── get_token.sql
    ├── sql.gleam                    # squirrel-generated
    ├── webhook.gleam                # webhook router + verification
    ├── db/
    │   └── pool.gleam               # pog pool child spec
    ├── workers/
    │   ├── woo_order.gleam          # bg_jobs worker
    │   └── shipstation.gleam        # bg_jobs worker
    └── integrations/
        ├── woocommerce.gleam        # REST client
        ├── shipstation.gleam        # REST client
        └── quickbooks.gleam         # OAuth2 + REST client
```

## Implementation Plan

### Phase 1: Project Scaffolding

Create project with `gleam new hasgier`, add dependencies, set up Dockerfile and `.env.example`.

### Phase 2: Database Schema

**`priv/migrations/001_initial_schema.sql`** — four tables:

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

Squirrel `.sql` files each contain a single query that squirrel turns into a typed Gleam function.

### Phase 3: Config Module

**`src/config.gleam`** — reads from `envoy`:

```gleam
pub type Config {
  Config(
    port: Int,
    database_url: String,
    woo_url: String,
    woo_consumer_key: String,
    woo_consumer_secret: String,
    woo_webhook_secret: String,
    shipstation_api_key: String,
    shipstation_api_secret: String,
    qb_client_id: String,
    qb_client_secret: String,
    qb_redirect_uri: String,
    qb_environment: String,
  )
}
```

### Phase 4: API Clients

#### `src/integrations/woocommerce.gleam`

~150 lines. REST wrapper for `https://{store}/wp-json/wc/v3/`. Uses consumer key/secret as query params or Basic Auth.

```gleam
fn get_order(conn: Connection, id: Int) -> Result(Dynamic, ApiError)
fn update_order(conn: Connection, id: Int, data: Dynamic) -> Result(Dynamic, ApiError)
fn list_orders(conn: Connection, params: List(#(String, String))) -> Result(List(Dynamic), ApiError)
```

#### `src/integrations/shipstation.gleam`

~100 lines. REST wrapper for `https://ssapi.shipstation.com/`. Uses Basic Auth (API key + API secret).

```gleam
fn get_shipment(conn: Connection, shipment_id: Int) -> Result(Dynamic, ApiError)
fn list_orders(conn: Connection, params: Dict(String, String)) -> Result(List(Dynamic), ApiError)
```

#### `src/integrations/quickbooks.gleam`

~250 lines. The heaviest module. Two parts:

**Part A — OAuth2** (using `flwr_oauth2`):
- `fn get_authorization_url() -> String` — builds the Intuit authorize URL with scopes
- `fn exchange_code(code: String, realm_id: String) -> Result(TokenData, ApiError)` — exchanges authorization code for tokens, stores to DB
- `fn refresh_token() -> Result(TokenData, ApiError)` — uses refresh token, updates DB
- `fn get_valid_token() -> Result(String, ApiError)` — returns current access token, refreshes if expired

**Part B — REST API**:
```gleam
fn create_invoice(conn: Connection, invoice_data: Json) -> Result(Dynamic, ApiError)
fn create_payment(conn: Connection, payment_data: Json) -> Result(Dynamic, ApiError)
fn query(conn: Connection, query: String) -> Result(Dynamic, ApiError)
fn find_customer(conn: Connection, email: String) -> Result(Dynamic, ApiError)
fn create_customer(conn: Connection, customer_data: Json) -> Result(Dynamic, ApiError)
```

All QB requests include `Authorization: Bearer {token}` and `Accept: application/json`. Token refresh is transparent via a middleware pattern.

### Phase 5: Webhook Router

**`src/webhook.gleam`** — ~150 lines. Handles all three webhook sources.

```gleam
fn handle_request(req: Request, ctx: Context) -> Response {
  case req.method, req.path {
    Post, "/webhooks/woocommerce" -> handle_woo_webhook(req, ctx)
    Post, "/webhooks/shipstation" -> handle_ss_webhook(req, ctx)
    Get, "/oauth/qb/callback" -> handle_qb_callback(req, ctx)
    Get, "/oauth/qb/authorize" -> handle_qb_authorize(ctx)
    _, _ -> wisp.not_found()
  }
}
```

**WooCommerce webhook handler:**
1. Read raw body as `BitArray` (`wisp.read_body_bits`)
2. Get `X-WC-Webhook-Signature` header
3. Compute `gleam_crypto.hmac(body, Sha256, webhook_secret) |> bit_array.base16_encode`
4. Constant-time compare with signature → return 401 on mismatch
5. Insert idempotency key → skip if duplicate
6. Return `202 Accepted` immediately
7. `bg_jobs.enqueue("woo_order_worker", payload)`

**ShipStation webhook handler:**
1. Verify Basic Auth header matches configured API credentials
2. Read raw body as `BitArray`
3. Decompress with Erlang's `:zlib.gunzip` (via `gleam_erlang` FFI)
4. Parse JSON, check `X-ShipStation-Event` header
5. Insert idempotency key → skip if duplicate
6. Return `200 OK` immediately
7. `bg_jobs.enqueue("shipstation_worker", payload)`

### Phase 6: Background Workers

#### `src/workers/woo_order.gleam`

~100 lines. Triggers on `order.created` and `order.updated` webhooks.

1. Extract order from webhook payload
2. Find or create QuickBooks customer by email
3. Map WooCommerce line items → QB invoice lines using entity_mappings table
4. Map shipping method → QB shipping item
5. Map payment method → QB payment account
6. POST invoice to QuickBooks
7. If order is paid, POST payment to QuickBooks
8. Upsert sync_state row with status `synced` + qb_invoice_id

#### `src/workers/shipstation.gleam`

~100 lines. Triggers on `SHIP_NOTIFY` and `ITEM_SHIP_NOTIFY`.

1. Extract shipment details + tracking number
2. Look up sync_state by order number (WooCommerce order number)
3. Fetch the QB invoice via qb_invoice_id
4. Update invoice with tracking info (ship date, carrier, tracking #)
5. Update sync_state with tracking_number and status

### Phase 7: Main Application

**`src/hasgier.gleam`** — ~80 lines. Sets up the OTP supervision tree:

```gleam
pub fn main() {
  let config = config.load()

  // 1. Start pog database pool (supervised)
  // 2. Ensure cigogne migrations are applied
  // 3. Start bg_jobs with queues + workers
  // 4. Start Wisp HTTP server on config.port
}
```

The supervision tree:
```
application_supervisor (OneForOne)
├── pog pool (permanent)
├── bg_jobs supervisor
│   ├── "default" queue
│   │   ├── woo_order_worker
│   │   └── shipstation_worker
│   └── monitor
└── Wisp/Mist HTTP server
```

### Phase 8: Deployment

Dockerfile builds to an ~80MB image. Fly.io deployment:

```bash
fly launch --build-only            # creates fly.toml
fly pg create --name hasgier-db    # managed Postgres
fly secrets set ...                # all API keys/tokens
fly deploy
```

`fly.toml` auto-detected, just configure port 8080 and attach Postgres.

## Build Order

| Step | Files | Effort | Depends on |
|---|---|---|---|
| 1. Project scaffold | `gleam new`, `gleam.toml` | 5 min | nothing |
| 2. DB schema + cigogne | 001 migration, cigogne.toml | 15 min | step 1 |
| 3. Config module | `config.gleam` | 10 min | step 1 |
| 4. DB pool | `db/pool.gleam` | 10 min | steps 1-2 |
| 5. WooCommerce client | `integrations/woocommerce.gleam` | 30 min | step 3 |
| 6. ShipStation client | `integrations/shipstation.gleam` | 20 min | step 3 |
| 7. QuickBooks client | `integrations/quickbooks.gleam` | 60 min | steps 3-4 |
| 8. Webhook router | `webhook.gleam` | 40 min | steps 4-7 |
| 9. BG workers | `workers/*.gleam` | 30 min | steps 4-7 |
| 10. Main + supervisor | `hasgier.gleam` | 20 min | steps 4-9 |
| 11. Dockerfile | `Dockerfile` | 10 min | step 10 |
| 12. Squirrel SQL files | `sql/*.sql` | 15 min | step 2 |

Total build time: ~4-5 hours for a working MVP.

## Gotchas

1. **ShipStation gzip**: The `:zlib.gunzip` FFI call is the one slightly awkward piece. Use `gleam_erlang` to call `:zlib.gunzip` directly.

2. **QuickBooks token refresh**: Must be atomic — store an `expires_at` 5 minutes before actual expiry and refresh early to avoid race conditions between workers.

3. **Webhook body double-read**: Wisp's `require_string_body` can only be called once per request. For WooCommerce you need the raw `BitArray` (for HMAC) AND the parsed JSON (for the payload). Read once as bits, store, then parse.

4. **WooCommerce HMAC comparison**: Use constant-time comparison. Fall back to Erlang's `:crypto` if `gleam_crypto` doesn't expose it.

5. **bg_jobs is pre-1.0**: Test thoroughly. The API is stable enough but edge cases may exist.
