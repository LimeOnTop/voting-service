package votestore

import "github.com/redis/go-redis/v9"

// guardScript applies rate limit and optional per-poll IP quota; returns 0/1/2.
var guardScript = redis.NewScript(`
local rateKey     = KEYS[1]
local quotaKey    = KEYS[2]
local rateLimit   = tonumber(ARGV[1])
local rateWindow  = tonumber(ARGV[2])
local quotaLimit  = tonumber(ARGV[3])
local quotaWindow = tonumber(ARGV[4])

if rateLimit > 0 then
  local hits = redis.call('INCR', rateKey)
  if hits == 1 then
    redis.call('EXPIRE', rateKey, rateWindow)
  end
  if hits > rateLimit then
    return 1
  end
end

if quotaLimit > 0 then
  local used = redis.call('INCR', quotaKey)
  if used == 1 then
    redis.call('EXPIRE', quotaKey, quotaWindow)
  end
  if used > quotaLimit then
    return 2
  end
end

return 0
`)

// castBallotScript claims the dedup slot and increments options; returns 1 or 0.
var castBallotScript = redis.NewScript(`
local votedKey  = KEYS[1]
local countsKey = KEYS[2]
local dedupTTL  = tonumber(ARGV[1])
local retention = tonumber(ARGV[2])

if redis.call('SET', votedKey, '1', 'NX', 'EX', dedupTTL) == false then
  return 0
end

local fresh = redis.call('EXISTS', countsKey) == 0
for i = 3, #ARGV do
  redis.call('HINCRBY', countsKey, ARGV[i], 1)
end
if fresh then
  redis.call('EXPIRE', countsKey, retention)
end

return 1
`)
