CREATE TABLE cms_users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(64) NOT NULL,
    password_hash VARCHAR(72) NOT NULL,
    role          VARCHAR(16) NOT NULL CHECK (role IN ('admin','user','viewer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_cms_users_username ON cms_users(username) WHERE deleted_at IS NULL;
