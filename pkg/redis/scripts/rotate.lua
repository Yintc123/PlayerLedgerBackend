-- rotate.lua — refresh 的核心 CAS。回传 {code, state_json}
--   code: 1=rotated / 2=grace_hit / 3=replay / 4=family_not_found
-- KEYS[1] = auth:family:{userID}:fid
-- KEYS[2] = auth:user_families:{userID}
-- ARGV[1] = presented_jti
-- ARGV[2] = new_jti
-- ARGV[3] = now_unix
-- ARGV[4] = grace_window_seconds
-- ARGV[5] = fid（给 replay 路径 SREM 索引用）
local raw = redis.call("GET", KEYS[1])
if not raw then return {4, ""} end

local f = cjson.decode(raw)
local now = tonumber(ARGV[3])
local grace_until = tonumber(ARGV[4]) + now
local abs_ttl_remaining = tonumber(f.abs_exp) - now

-- abs_exp 過期 → 视同 family 不存在（理论上 Redis TTL 已先到，但双保险）
if abs_ttl_remaining <= 0 then
    redis.call("DEL", KEYS[1])
    redis.call("SREM", KEYS[2], ARGV[5])
    return {4, ""}
end

-- 正常 rotation
if f.current_jti == ARGV[1] then
    f.previous_jti = f.current_jti
    f.previous_response_until = grace_until
    f.current_jti = ARGV[2]
    f.last_rotated_at = now
    redis.call("SET", KEYS[1], cjson.encode(f), "EX", abs_ttl_remaining)
    return {1, cjson.encode(f)}
end

-- Grace window 命中（網路重试）
if f.previous_jti and f.previous_jti == ARGV[1]
   and tonumber(f.previous_response_until or 0) > now then
    return {2, cjson.encode(f)}
end

-- 重放偵測 → 廢掉 family + 同步清掉索引
redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[2], ARGV[5])
return {3, cjson.encode(f)}
