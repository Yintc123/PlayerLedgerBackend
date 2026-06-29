-- 玩家查詢欄位擴充（players-model.md §10）。
-- 鎖與資料改寫注意事項（目前 members 資料量小，以下皆可接受）：
--  1. ADD COLUMN ... DEFAULT 'active'：PostgreSQL 11+ 為 metadata-only，快速、不改寫整表。
--  2. UPDATE 回填 display_name 會改寫每一列；ALTER COLUMN SET NOT NULL 需全表掃描 + 短暫
--     ACCESS EXCLUSIVE 鎖。表大時應改線上回填模式。本期表小，直接執行。

CREATE TYPE member_status AS ENUM ('active', 'frozen', 'closed');

ALTER TABLE members
    ADD COLUMN external_id    VARCHAR(64),
    ADD COLUMN display_name   VARCHAR(64),
    ADD COLUMN email          VARCHAR(255),
    ADD COLUMN phone          VARCHAR(32),
    ADD COLUMN status         member_status NOT NULL DEFAULT 'active',
    ADD COLUMN last_active_at TIMESTAMPTZ;

-- display_name 回填既有列後設 NOT NULL（避免既有資料違反約束）
UPDATE members SET display_name = username WHERE display_name IS NULL;
ALTER TABLE members ALTER COLUMN display_name SET NOT NULL;

CREATE UNIQUE INDEX uq_members_external_id
    ON members (external_id)
    WHERE external_id IS NOT NULL AND deleted_at IS NULL;

-- 大小寫不敏感前綴：lower() 函式索引 + text_pattern_ops（配 LIKE，非 ILIKE）
CREATE INDEX idx_members_display_name_lower
    ON members (lower(display_name) text_pattern_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_members_email_lower
    ON members (lower(email) text_pattern_ops)
    WHERE email IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX idx_members_phone
    ON members (phone)
    WHERE phone IS NOT NULL AND deleted_at IS NULL;
