-- migrations/000003_seed_initial_admin.down.sql
-- 回滚：删除初始 admin 用户

DELETE FROM cms_users WHERE username = 'admin';
