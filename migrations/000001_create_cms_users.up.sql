CREATE TABLE IF NOT EXISTS cms_users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(64) NOT NULL,
    password_hash VARCHAR(72) NOT NULL,
    role VARCHAR(16) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_cms_users_username 
ON cms_users(username) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_cms_users_deleted_at ON cms_users(deleted_at);
