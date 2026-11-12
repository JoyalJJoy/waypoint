-- +goose Up
CREATE TYPE order_status AS ENUM ('pending', 'confirmed', 'processing', 'completed', 'cancelled');
CREATE TYPE reservation_status AS ENUM ('held', 'released', 'fulfilled', 'expired');

CREATE TABLE orders (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    order_number     text NOT NULL,
    status           order_status NOT NULL DEFAULT 'pending',
    warehouse_id     uuid NOT NULL,
    customer_name    text NOT NULL,
    customer_email   citext NOT NULL,
    shipping_address jsonb NOT NULL DEFAULT '{}'::jsonb,
    currency         char(3) NOT NULL,
    subtotal_cents   bigint NOT NULL DEFAULT 0 CHECK (subtotal_cents >= 0),
    total_cents      bigint NOT NULL DEFAULT 0 CHECK (total_cents >= 0),
    notes            text NOT NULL DEFAULT '',
    created_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    version          bigint NOT NULL DEFAULT 0,
    confirmed_at     timestamptz,
    cancelled_at     timestamptz,
    completed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, order_number),
    FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);

CREATE INDEX orders_list_idx ON orders (tenant_id, status, created_at DESC);
CREATE INDEX orders_customer_idx ON orders (tenant_id, customer_email);

CREATE TABLE order_items (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    order_id         uuid NOT NULL,
    product_id       uuid NOT NULL,
    sku              text NOT NULL,
    name             text NOT NULL,
    quantity         integer NOT NULL CHECK (quantity > 0),
    unit_price_cents bigint NOT NULL CHECK (unit_price_cents >= 0),
    total_cents      bigint NOT NULL CHECK (total_cents >= 0),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id)
);

CREATE INDEX order_items_order_idx ON order_items (tenant_id, order_id);

CREATE TABLE order_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    order_id    uuid NOT NULL,
    from_status order_status,
    to_status   order_status NOT NULL,
    actor_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    note        text NOT NULL DEFAULT '',
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX order_events_order_idx ON order_events (tenant_id, order_id, id);

CREATE TABLE stock_reservations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    warehouse_id  uuid NOT NULL,
    product_id    uuid NOT NULL,
    order_id      uuid NOT NULL,
    order_item_id uuid NOT NULL,
    quantity      integer NOT NULL CHECK (quantity > 0),
    status        reservation_status NOT NULL DEFAULT 'held',
    expires_at    timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    created_at    timestamptz NOT NULL DEFAULT now(),
    released_at   timestamptz,
    fulfilled_at  timestamptz,
    FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id),
    FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, order_item_id) REFERENCES order_items (tenant_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX stock_reservations_held_idx
    ON stock_reservations (tenant_id, order_id, product_id)
    WHERE status = 'held';
CREATE INDEX stock_reservations_expiry_idx
    ON stock_reservations (status, expires_at)
    WHERE status = 'held';

CREATE TRIGGER orders_touch_updated_at
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE orders FORCE ROW LEVEL SECURITY;
CREATE POLICY orders_tenant ON orders
    FOR ALL USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

ALTER TABLE order_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_items FORCE ROW LEVEL SECURITY;
CREATE POLICY order_items_tenant ON order_items
    FOR ALL USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

ALTER TABLE order_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_events FORCE ROW LEVEL SECURITY;
CREATE POLICY order_events_read ON order_events
    FOR SELECT USING (tenant_id = app_current_tenant());
CREATE POLICY order_events_insert ON order_events
    FOR INSERT WITH CHECK (tenant_id = app_current_tenant());

ALTER TABLE stock_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY stock_reservations_tenant ON stock_reservations
    FOR ALL USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

-- +goose Down
DROP TABLE IF EXISTS stock_reservations;
DROP TABLE IF EXISTS order_events;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TYPE IF EXISTS reservation_status;
DROP TYPE IF EXISTS order_status;
