DROP INDEX IF EXISTS idx_members_phone;
DROP INDEX IF EXISTS idx_members_email_lower;
DROP INDEX IF EXISTS idx_members_display_name_lower;
DROP INDEX IF EXISTS uq_members_external_id;

ALTER TABLE members
    DROP COLUMN IF EXISTS last_active_at,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS phone,
    DROP COLUMN IF EXISTS email,
    DROP COLUMN IF EXISTS display_name,
    DROP COLUMN IF EXISTS external_id;

DROP TYPE IF EXISTS member_status;
