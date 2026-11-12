-- +goose Up
CREATE TABLE idempotency_keys (
    tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    key             text NOT NULL,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_hash    text NOT NULL,
    status          text NOT NULL DEFAULT 'in_progress' CHECK (status IN ('in_progress', 'completed')),
    response_status integer,
    response_body   jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz,
    PRIMARY KEY (tenant_id, key)
);

ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY idempotency_keys_tenant ON idempotency_keys
    FOR ALL USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

-- The transactional outbox. Business changes and their events commit in the
-- same transaction; a separate relay publishes pending rows to NATS. This
-- removes the classic dual-write failure mode where the database commits but
-- the event publish fails (or vice versa).
CREATE TABLE outbox_events (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_id       uuid NOT NULL UNIQUE,
    aggregate_type text NOT NULL,
    aggregate_id   uuid NOT NULL,
    event_type     text NOT NULL,
    payload        jsonb NOT NULL,
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    published_at   timestamptz,
    attempts       integer NOT NULL DEFAULT 0,
    last_error     text
);

CREATE INDEX outbox_events_pending_idx ON outbox_events (id) WHERE published_at IS NULL;
CREATE INDEX outbox_events_tenant_idx ON outbox_events (tenant_id, occurred_at DESC);

-- +goose Down
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS idempotency_keys;
