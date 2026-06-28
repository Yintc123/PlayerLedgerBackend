-- save.lua — login 时建立 family + 更新索引
-- KEYS[1] = auth:family:{userID}:fid
-- KEYS[2] = auth:user_families:{userID}
-- ARGV[1] = family_state_json（内含 abs_exp）
-- ARGV[2] = abs_ttl_seconds（= abs_exp - now，由 caller 计算传入）
-- ARGV[3] = fid
redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
redis.call("SADD", KEYS[2], ARGV[3])
return 1
