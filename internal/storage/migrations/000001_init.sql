CREATE TABLE inventory (
    product_id UUID PRIMARY KEY,
    available_qty BIGINT NOT NULL DEFAULT 0 CHECK (available_qty >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE reservations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL,
    product_id UUID NOT NULL,
    qty BIGINT NOT NULL CHECK (qty > 0),
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'confirmed', 'cancelled', 'expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT reservations_order_product_unique UNIQUE (order_id, product_id)
);

CREATE INDEX reservations_pending_expiry_idx
    ON reservations (expires_at)
    WHERE status = 'pending';

CREATE INDEX reservations_order_idx ON reservations (order_id);
