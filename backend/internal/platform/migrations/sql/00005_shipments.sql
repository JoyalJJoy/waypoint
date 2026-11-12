-- +goose Up
CREATE TYPE shipment_status AS ENUM (
    'pending',
    'assigned',
    'picked_up',
    'in_transit',
    'out_for_delivery',
    'delivered',
    'failed',
    'cancelled'
);

CREATE TABLE shipments (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    shipment_number       text NOT NULL,
    order_id              uuid NOT NULL,
    warehouse_id          uuid NOT NULL,
    tracking_number       text NOT NULL,
    carrier               text NOT NULL,
    service_level         text NOT NULL DEFAULT 'ground',
    status                shipment_status NOT NULL DEFAULT 'pending',
    assigned_agent_id     uuid REFERENCES users(id) ON DELETE SET NULL,
    weight_grams          integer NOT NULL DEFAULT 0 CHECK (weight_grams >= 0),
    estimated_delivery_at timestamptz,
    picked_up_at          timestamptz,
    delivered_at          timestamptz,
    cancelled_at          timestamptz,
    version               bigint NOT NULL DEFAULT 0,
    created_by            uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, shipment_number),
    UNIQUE (tenant_id, order_id),
    UNIQUE (tracking_number),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);

CREATE INDEX shipments_list_idx ON shipments (tenant_id, status, created_at DESC);
CREATE INDEX shipments_agent_idx ON shipments (tenant_id, assigned_agent_id, status);

-- Append-only tracking timeline. The public API exposes a sanitized projection
-- of these rows; actor IDs and internal notes stay private.
CREATE TABLE shipment_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    shipment_id uuid NOT NULL,
    status      shipment_status NOT NULL,
    description text NOT NULL DEFAULT '',
    latitude    double precision CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
    longitude   double precision CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180),
    actor_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, shipment_id) REFERENCES shipments (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX shipment_events_shipment_idx ON shipment_events (tenant_id, shipment_id, id);

CREATE TRIGGER shipments_touch_updated_at
    BEFORE UPDATE ON shipments
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

ALTER TABLE shipments ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipments FORCE ROW LEVEL SECURITY;
CREATE POLICY shipments_tenant ON shipments
    FOR ALL USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

ALTER TABLE shipment_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipment_events FORCE ROW LEVEL SECURITY;
CREATE POLICY shipment_events_read ON shipment_events
    FOR SELECT USING (tenant_id = app_current_tenant());
CREATE POLICY shipment_events_insert ON shipment_events
    FOR INSERT WITH CHECK (tenant_id = app_current_tenant());

-- Public tracking is intentionally a SECURITY DEFINER function rather than a
-- narrow RLS policy: the public has no tenant context. The function returns a
-- single shipment by its unguessable tracking number and exposes only fields
-- that are safe for a customer-facing page.
CREATE FUNCTION public_tracking(p_tracking_number text)
RETURNS jsonb
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
    SELECT jsonb_build_object(
        'tracking_number', s.tracking_number,
        'status', s.status,
        'carrier', s.carrier,
        'service_level', s.service_level,
        'order_number', o.order_number,
        'destination', jsonb_build_object(
            'city', COALESCE(o.shipping_address->>'city', ''),
            'region', COALESCE(o.shipping_address->>'region', ''),
            'country', COALESCE(o.shipping_address->>'country', '')
        ),
        'estimated_delivery_at', s.estimated_delivery_at,
        'picked_up_at', s.picked_up_at,
        'delivered_at', s.delivered_at,
        'events', COALESCE((
            SELECT jsonb_agg(jsonb_build_object(
                'status', e.status,
                'description', e.description,
                'latitude', e.latitude,
                'longitude', e.longitude,
                'created_at', e.created_at
            ) ORDER BY e.id)
            FROM shipment_events e
            WHERE e.tenant_id = s.tenant_id AND e.shipment_id = s.id
        ), '[]'::jsonb),
        'created_at', s.created_at,
        'updated_at', s.updated_at
    )
    FROM shipments s
    JOIN orders o ON o.tenant_id = s.tenant_id AND o.id = s.order_id
    WHERE s.tracking_number = p_tracking_number
$$;

REVOKE ALL ON FUNCTION public_tracking(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public_tracking(text) TO waypoint_app;

-- +goose Down
DROP FUNCTION IF EXISTS public_tracking(text);
DROP TABLE IF EXISTS shipment_events;
DROP TABLE IF EXISTS shipments;
DROP TYPE IF EXISTS shipment_status;
