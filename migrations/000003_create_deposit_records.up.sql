CREATE TYPE deposit_status AS ENUM (
    'pending', 'completed', 'failed', 'cancelled', 'refunded'
);
CREATE TYPE payment_method AS ENUM (
    'bank_transfer', 'credit_card', 'manual', 'convenience_store', 'e_wallet'
);

CREATE TABLE deposit_records (
    id             UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id      UUID           NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
    player_name    VARCHAR(64)    NOT NULL,
    amount         BIGINT         NOT NULL CHECK (amount > 0),
    currency       CHAR(3)        NOT NULL DEFAULT 'TWD' CHECK (currency IN ('TWD')),
    status         deposit_status NOT NULL DEFAULT 'pending',
    payment_method payment_method NOT NULL,
    operator_id    UUID           REFERENCES cms_users(id),
    operator_ip    INET,
    internal_note  TEXT,
    display_note   TEXT,
    reference_no   VARCHAR(128),
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT now()
);

CREATE INDEX idx_deposit_records_player_created
    ON deposit_records (player_id, created_at DESC);

CREATE INDEX idx_deposit_records_status_created
    ON deposit_records (status, created_at DESC);

CREATE UNIQUE INDEX uq_deposit_records_reference_no
    ON deposit_records (reference_no)
    WHERE reference_no IS NOT NULL;

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_deposit_records_updated_at
    BEFORE UPDATE ON deposit_records
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
