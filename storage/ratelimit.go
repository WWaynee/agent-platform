package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// ============ Redis 分布式滑动窗口限流 ============
//
// 为什么要用 Redis：多实例部署时长驻限流必须用分布式计数，不能用本地内存
// （否则每个实例各自计数，整体阈值失效、不公平）。
//
// 算法：滑动窗口（Sorted Set / ZSet）。
//   - key 内的每个 member 是一次请求的时间戳（score 也等于时间戳，用于按窗口裁剪）。
//   - 每次请求：原子地删掉窗口外的旧请求 → 统计窗口内请求数 → 超阈值拒绝，否则写入当前请求。
//   - 用 Lua 脚本保证原子性（删旧 + 计数 + 写入 + 设过期一条龙），避免并发竞态。
//
// key 设计：
//   - 租户级：ratelimit:tenant:{tenant_id}
//   - 用户级：ratelimit:user:{user_id}
//
// 返回值：true 表示放行（计算并记入本次请求），false 表示超限拦截（本次不计入）。

// rateLimitScript 滑动窗口限流 Lua 脚本。
// KEYS[1] = 限流 key（ZSet）
// ARGV[1] = 当前时间戳（毫秒）
// ARGV[2] = 窗口时长（毫秒）
// ARGV[3] = 窗口内允许的最大请求数
// ARGV[4] = key 过期时间（毫秒，用于自动清理，比窗口略大即可）
// ARGV[5] = 本条请求的成员唯一值（member，避免同毫秒并发使成员重复覆盖计数少算）
//
// 返回 1 = 放行（已计入）；返回 0 = 超限拦截（不计入）。
var rateLimitScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local member = ARGV[5]

-- 1. 清理窗口外（过期）的旧请求
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
-- 2. 统计当前窗口内请求数
local count = redis.call('ZCARD', key)
if count >= limit then
  return 0
end
-- 3. 写入本次请求（score=now，member=唯一值）
redis.call('ZADD', key, now, member)
-- 4. 设置/刷新过期时间，key 空闲后自动清理
redis.call('PEXPIRE', key, ttl)
return 1
`

// AllowRequest 判断某维度（租户/用户）的某次请求是否被限流。
//   - keyPrefix：前缀（ratelimit:tenant: / ratelimit:user:）
//   - identity：租户 ID / 用户 ID
//   - limit：窗口内最大请求数
//   - window：窗口时长
//   - keyTTL：key 过期时长（自动清理）
//
// 返回 true=允许（已计入窗口），false=拒绝（超限）。
// Redis 不可用时保守放行（不因限流组件故障把整个服务搞挂），但打印告警。
func AllowRequest(ctx context.Context, keyPrefix string, identity uint64, limit int, window, keyTTL time.Duration) (bool, error) {
	// 限流阈值非正（0 或负数）表示不限制 —— 兼容把阈值配为 0 的场景
	if limit <= 0 {
		return true, nil
	}
	if RDB == nil {
		return true, nil // Redis 未初始化，无从限流，保守放行
	}

	key := fmt.Sprintf("%s%d", keyPrefix, identity)
	nowMs := time.Now().UnixMilli()

	res, err := RDB.Eval(ctx, rateLimitScript, []string{key},
		nowMs,
		window.Milliseconds(),
		limit,
		keyTTL.Milliseconds(),
		uniqueMember(nowMs),
	).Int()
	if err != nil {
		// 限流组件故障：打印告警并放行，避免影响业务（宁可放过不可不放服务）
		// fmt.Printf("[ratelimit] 限流判断失败（放行兜底）: %v\n", err)
		return true, err
	}
	return res == 1, nil
}

// 两个限流 key 前缀常量，供中间件使用
const (
	// RateLimitTenantKeyPrefix 租户级限流 key 前缀
	RateLimitTenantKeyPrefix = "ratelimit:tenant:"
	// RateLimitUserKeyPrefix 用户级限流 key 前缀
	RateLimitUserKeyPrefix = "ratelimit:user:"
)

// uniqueMember 生成本次请求 ZSet 成员的唯一值。
// 用"时间戳 + 随机串"保证同一毫秒并发时成员也不重复，避免 ZADD 相互覆盖导致少计数。
func uniqueMember(nowMs int64) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d:%s", nowMs, hex.EncodeToString(b))
}
