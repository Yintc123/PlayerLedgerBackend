DROP TRIGGER IF EXISTS trg_deposit_records_updated_at ON deposit_records;
DROP TABLE IF EXISTS deposit_records;
DROP TYPE IF EXISTS payment_method;
DROP TYPE IF EXISTS deposit_status;
-- set_updated_at() 為共用函式，不在此 down migration 刪除
