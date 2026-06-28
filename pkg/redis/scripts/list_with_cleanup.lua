-- list_with_cleanup.lua — ListByUser 的 lazy cleanup：回传所有存活 family，顺手 SREM 孤儿 fid
-- KEYS[1] = auth:user_families:{userID}
-- ARGV[1] = user_id
local fids = redis.call("SMEMBERS", KEYS[1])
local result = {}
for _, fid in ipairs(fids) do
    local raw = redis.call("GET", "auth:family:{" .. ARGV[1] .. "}:" .. fid)
    if raw then
        table.insert(result, raw)
    else
        redis.call("SREM", KEYS[1], fid)
    end
end
return result
