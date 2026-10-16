CREATE TABLE webhook_events (
  id SERIAL PRIMARY KEY,
  idem_key TEXT UNIQUE NOT NULL,
  source TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'processing',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE qb_tokens (
  id SERIAL PRIMARY KEY,
  realm_id TEXT UNIQUE NOT NULL,
  access_token TEXT NOT NULL,
  refresh_token TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE entity_mappings (
  id SERIAL PRIMARY KEY,
  source_type TEXT NOT NULL,
  source_id TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  UNIQUE(source_type, source_id, target_type)
);

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
