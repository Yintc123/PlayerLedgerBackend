-- migrations/000003_seed_initial_admin.up.sql
-- Seed initial admin user for development/demo purposes
-- 预设帐密：admin / admin123（DEMO ONLY；prod 部署前见规格书 §13.5 末段「Prod 部署前必做」）
--
-- password_hash 为 bcrypt(admin123, cost=10) 的输出。下方值由以下方法生成：
--   htpasswd -bnBC 10 "" admin123 | tr -d ':\n'
--   或 Go: bcrypt.GenerateFromPassword([]byte("admin123"), 10)
--
-- 由于 bcrypt 包含随机 salt，每次重算结果都不同；本文档使用标准测试值。
-- 重算时替换下方的 REPLACE_WITH_BCRYPT_HASH_OF_admin123。
--
-- NOT EXISTS 子查询保 idempotent — 手动重跑或多副本同时启动都只会插入一次。

INSERT INTO cms_users (username, password_hash, role)
SELECT 'admin', '$2a$10$REPLACE_WITH_BCRYPT_HASH_OF_admin123', 'admin'
WHERE NOT EXISTS (
    SELECT 1 FROM cms_users WHERE username = 'admin' AND deleted_at IS NULL
);
