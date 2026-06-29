-- revoke.lua — 单一 family 登出
-- KEYS[1] = auth:family:{userID}:fid
-- KEYS[2] = auth:user_families:{userID}
-- ARGV[1] = fid
-- 回传 DEL 的命中数（1 = family 存在并被删除；0 = family 不存在），
-- 供 caller 区分「撤销成功」与「fid 不存在 → 404」（对齐 OpenAPI / ADR 007）。
local deleted = redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[2], ARGV[1])
return deleted
