-- migrations/000003_seed_initial_admin.up.sql
-- 預設帳密：admin / admin123（DEMO ONLY；prod 部署前見 §13.5 末段「Prod 部署前必做」）。
-- password_hash 為 bcrypt(admin123, cost=10) 的輸出；每次重算 salt 不同，hash 不同。
-- 重算方法（任選其一）：
--   htpasswd -bnBC 10 "" admin123 | tr -d ':\n'
--   Go: bcrypt.GenerateFromPassword([]byte("admin123"), 10)
--
-- NOT EXISTS 子查詢保 idempotent — 手動重跑或多副本同時啟動都只會插入一次。
INSERT INTO cms_users (username, password_hash, role)
SELECT 'admin', '$2a$10$REPLACE_WITH_BCRYPT_HASH_OF_admin123', 'admin'
WHERE NOT EXISTS (
    SELECT 1 FROM cms_users WHERE username = 'admin' AND deleted_at IS NULL
);
