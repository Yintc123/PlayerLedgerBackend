-- revoke.lua — 单一 family 登出
-- KEYS[1] = auth:family:{userID}:fid
-- KEYS[2] = auth:user_families:{userID}
-- ARGV[1] = fid
redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[2], ARGV[1])
return 1
