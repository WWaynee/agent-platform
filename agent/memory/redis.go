package memory

import (
	"agent-platform/observability"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisMemory 是 Memory 接口的 Redis 实现。
// 用 Redis 的 List 结构存每个会话的消息序列：RPUSH 追加、LRANGE 读取、LTRIM 截断，
// 天然保持消息时间正序；数据落 Redis，进程重启不丢（靠 Redis 持久化），实现"重启不丢数据"。
//
// 每个会话一个 key，一条消息 = 一个 list 元素（单条 JSON）。
//
// 面向接口实现：engine 只依赖 Memory 接口，从内存版切到 Redis 版业务代码零改动。

// ============ Key 设计（命名规范统一） ============
// 会话消息 key 格式：session:{tenant_id}:{session_id}:messages
// 示例：session:9988:t-001:messages
//
// 为什么 key 里带 tenant_id（多租户安全底线）：
//   - 就算不同租户用了相同的 session_id，key 也因 tenant_id 不同而不同，天然隔离、绝不混存；
//   - 便于按租户批量统计 / 清理（SCAN pattern: session:{tenant_id}:*）。
//
// 分隔符统一用 ":"，后续各段不允许再出现 ":"，避免歧义（session_id 由调用方保证不含冒号）。
const (
	// sessionKeyPrefix key 前缀段。
	sessionKeyPrefix = "session:"
	// sessionKeySuffix key 结尾段，标识"该 key 存的是会话消息列表"。
	sessionKeySuffix = ":messages"
)

// memoryTTL 会话记忆的存活时长。
// 加 TTL 防止长期不访问的会话无限堆积 Redis；同时足够长（7 天），
// 保证"重启不丢数据"的落地（即在有效期内重启后历史仍可读回）。
const memoryTTL = 7 * 24 * time.Hour

// slowThreshold 慢查询日志阈值：Redis 操作耗时达到或超过该值即记"慢查询"日志。
const slowThreshold = 100 * time.Millisecond

// sessionKey 生成某租户某会话在 Redis 里的 key。
// 格式：session:{tenant_id}:{session_id}:messages
func sessionKey(tenantID uint64, sessionID string) string {
	return fmt.Sprintf("%s%d:%s%s", sessionKeyPrefix, tenantID, sessionID, sessionKeySuffix)
}

// RedisMemory 持有一个 context 以承载 Redis 调用（Memory 接口方法不带 ctx）。
type RedisMemory struct {
	ctx context.Context
	rdb *redis.Client
}

// NewRedisMemory 构造 Redis 版记忆管理器。
// rdb 为已初始化并 Ping 通过的 go-redis 客户端（传入 storage.RDB 即可）。
func NewRedisMemory(rdb *redis.Client) *RedisMemory {
	return &RedisMemory{ctx: context.Background(), rdb: rdb}
}

// logRedis 记录一次 Redis 操作日志：
//   - 超时（超过 slowThreshold）→ 记慢查询日志（latency 字段）；
//   - 出错 → 记 error 日志；
//   - 异常但未超时 → 记 warn（此时已归入慢日志场景，否则忽略）。
//
// 由于 Memory 接口方法不带标准 context，拿不到 trace_id，这里按租户维度记录身份。
func (m *RedisMemory) logRedis(op string, tenantID uint64, sessionID string, start time.Time, err error) {
	latency := time.Since(start)
	logger := observability.WithTenantUser(tenantID, 0)
	fields := []zap.Field{
		zap.String("op", op),
		zap.String("session_id", sessionID),
		zap.Int64(observability.FieldLatency, latency.Milliseconds()),
	}
	if err != nil {
		logger.Error("Redis 操作失败", append(fields, zap.Error(err))...)
		return
	}
	if latency >= slowThreshold {
		// 慢查询：超过 100ms 的（含"无会话历史"的正常返回路径也可能因 LRANGE 大而变慢）
		logger.Warn("Redis 慢查询（>=100ms）", fields...)
	}
}

// GetHistory 从 Redis 读回某租户某会话的完整历史消息（LRANGE 0 -1，按追加顺序即时间正序）。
// 无历史或会话不存在时返回 nil。损坏/无法解析的条目跳过，不 panic。
func (m *RedisMemory) GetHistory(tenantID uint64, sessionID string) []ChatMessage {
	start := time.Now()
	vals, err := m.rdb.LRange(m.ctx, sessionKey(tenantID, sessionID), 0, -1).Result()
	m.logRedis("LRANGE", tenantID, sessionID, start, err)
	if err != nil || len(vals) == 0 {
		return nil
	}
	out := make([]ChatMessage, 0, len(vals))
	for _, v := range vals {
		var msg ChatMessage
		if err := json.Unmarshal([]byte(v), &msg); err != nil {
			continue // 跳过脏数据，不因单条损坏影响整体
		}
		out = append(out, msg)
	}
	return out
}

// AddMessage 向某租户某会话追加一条消息（RPUSH 到 list 末尾）。
// 追加后刷新会话 TTL，延长其存活时长。
func (m *RedisMemory) AddMessage(tenantID uint64, sessionID string, msg ChatMessage) {
	b, err := json.Marshal(msg)
	if err != nil {
		return // 序列化失败（理论不发生），静默跳过即可
	}
	key := sessionKey(tenantID, sessionID)

	start := time.Now()
	err = m.rdb.RPush(m.ctx, key, b).Err()
	if err == nil {
		// 刷新 TTL：每次活跃都延续存活期
		err = m.rdb.Expire(m.ctx, key, memoryTTL).Err()
	}
	m.logRedis("RPUSH/EXPIRE", tenantID, sessionID, start, err)
}

// Clear 清空某租户某会话的所有历史（DEL 该 key）。
func (m *RedisMemory) Clear(tenantID uint64, sessionID string) {
	start := time.Now()
	err := m.rdb.Del(m.ctx, sessionKey(tenantID, sessionID)).Err()
	m.logRedis("DEL", tenantID, sessionID, start, err)
}

// Truncate 超长时只保留某租户某会话最近 maxTokens 条消息（LTRIM 截断 list 尾部）。
// 简单"丢最旧"策略；更完整的 token 级摘要压缩后续在增强版实现。
func (m *RedisMemory) Truncate(tenantID uint64, sessionID string, maxTokens int) {
	if maxTokens <= 0 {
		return
	}
	start := time.Now()
	err := m.rdb.LTrim(m.ctx, sessionKey(tenantID, sessionID), -int64(maxTokens), -1).Err()
	m.logRedis("LTRIM", tenantID, sessionID, start, err)
}
