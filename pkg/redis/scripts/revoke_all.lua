-- revoke_all.lua — 全装置登出
-- KEYS[1] = auth:user_families:{userID}
-- ARGV[1] = user_id（用来组 family key 字串；hash tag 必须跟 KEYS[1] 同 slot）
local fids = redis.call("SMEMBERS", KEYS[1])
for _, fid in ipairs(fids) do
    redis.call("DEL", "auth:family:{" .. ARGV[1] .. "}:" .. fid)
end
redis.call("DEL", KEYS[1])
return #fids
