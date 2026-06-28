-- migrations/000003_seed_initial_admin.down.sql
DELETE FROM cms_users WHERE username = 'admin';
