-- +goose Up
CREATE TABLE warehouses (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    code       text NOT NULL,
    address    jsonb NOT NULL DEFAULT '{}'::jsonb,
    latitude   double precision,
    longitude  double precision,
    is_active  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, code)
);

CREATE TABLE products (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sku              text NOT NULL,
    name             text NOT NULL,
    description      text NOT NULL DEFAULT '',
    unit_price_cents bigint NOT NULL CHECK (unit_price_cents >= 0),
    currency         char(3) NOT NULL DEFAULT 'USD',
    weight_grams     integer CHECK (weight_grams IS NULL OR weight_grams >= 0),
    is_active        boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, sku)
);

CREATE TABLE stock_levels (
    tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    warehouse_id uuid NOT NULL,
    product_id   uuid NOT NULL,
    on_hand      integer NOT NULL DEFAULT 0 CHECK (on_hand >= 0),
    reserved     integer NOT NULL DEFAULT 0 CHECK (reserved >= 0 AND reserved <= on_hand),
    version      bigint NOT NULL DEFAULT 0,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, warehouse_id, product_id),
    FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX stock_levels_product_idx ON stock_levels (tenant_id, product_id);

CREATE TYPE stock_movement_kind AS ENUM (
    'receipt',
    'reservation',
    'release',
    'pick',
    'adjustment',
    'return'
);

-- The ledger is append-only. `quantity` is signed so an adjustment can be
-- negative; the kind determines which counters move. This gives us an audit
-- trail that can rebuild stock_levels if a bug ever corrupts them.
CREATE TABLE stock_movements (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    warehouse_id   uuid NOT NULL,
    product_id     uuid NOT NULL,
    kind           stock_movement_kind NOT NULL,
    quantity       integer NOT NULL CHECK (quantity <> 0),
    reason         text NOT NULL DEFAULT '',
    order_id       uuid,
    reservation_id uuid,
    actor_id       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX stock_movements_lookup_idx
    ON stock_movements (tenant_id, warehouse_id, product_id, created_at DESC);
CREATE INDEX stock_movements_recent_idx
    ON stock_movements (tenant_id, created_at DESC);

CREATE TRIGGER warehouses_touch_updated_at
    BEFORE UPDATE ON warehouses
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TRIGGER products_touch_updated_at
    BEFORE UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TRIGGER stock_levels_touch_updated_at
    BEFORE UPDATE ON stock_levels
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE warehouses ENABLE ROW LEVEL SECURITY;
ALTER TABLE warehouses FORCE ROW LEVEL SECURITY;
CREATE POLICY warehouses_tenant ON warehouses
    FOR ALL USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE ROW LEVEL SECURITY;
CREATE POLICY products_tenant ON products
    FOR ALL USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

ALTER TABLE stock_levels ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_levels FORCE ROW LEVEL SECURITY;
CREATE POLICY stock_levels_tenant ON stock_levels
    FOR ALL USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

ALTER TABLE stock_movements ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_movements FORCE ROW LEVEL SECURITY;
CREATE POLICY stock_movements_read ON stock_movements
    FOR SELECT USING (tenant_id = app_current_tenant());
CREATE POLICY stock_movements_insert ON stock_movements
    FOR INSERT WITH CHECK (tenant_id = app_current_tenant());

-- +goose Down
DROP TABLE IF EXISTS stock_movements;
DROP TYPE IF EXISTS stock_movement_kind;
DROP TABLE IF EXISTS stock_levels;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS warehouses;
