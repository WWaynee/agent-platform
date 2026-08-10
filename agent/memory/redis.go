package memory

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisMemory 是 Memory 接口的 Redis 实现。
// 用 Redis 的 List 结构存每个会话的消息序列：RPUSH 追加、LRANGE 读取、LTRIM 截断，
// 天然保持消息时间正序；数据落 Redis，进程重启不丢（靠 Redis 持久化），实现"重启不丢数据"。
//
// 每个会话一个 key：`agent:memory:{sessionID}`，一条消息 = 一个 list 元素（单条 JSON）。
//
// 面向接口实现：engine 只依赖 Memory 接口，从内存版切到 Redis 版业务代码零改动。

// sessionKeyPrefix 会话记忆的 key 前缀，避免与 Redis 中其他数据冲突。
// 每个会话的完整 key = 前缀 + 会话ID，例如 `agent:memory:t-001`。
const sessionKeyPrefix = "agent:memory:"

// memoryTTL 会话记忆的存活时长。
// 加 TTL 防止长期不访问的会话无限堆积 Redis；同时足够长（7 天），
// 保证"重启不丢数据"的落地（即在有效期内重启后历史仍可读回）。
const memoryTTL = 7 * 24 * time.Hour

// sessionKey 生成某会话在 Redis 里的 key。
func sessionKey(sessionID string) string { return sessionKeyPrefix + sessionID }

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

// GetHistory 从 Redis 读回某会话的完整历史消息（LRANGE 0 -1，按追加顺序即时间正序）。
// 无历史或会话不存在时返回 nil。损坏/无法解析的条目跳过，不 panic。
func (m *RedisMemory) GetHistory(sessionID string) []ChatMessage {
	vals, err := m.rdb.LRange(m.ctx, sessionKey(sessionID), 0, -1).Result()
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

// AddMessage 向某会话追加一条消息（RPUSH 到 list 末尾）。
// 追加后刷新会话 TTL，延长其存活时长。
func (m *RedisMemory) AddMessage(sessionID string, msg ChatMessage) {
	b, err := json.Marshal(msg)
	if err != nil {
		return // 序列化失败（理论不发生），静默跳过即可
	}
	key := sessionKey(sessionID)
	m.rdb.RPush(m.ctx, key, b)
	// 刷新 TTL：每次活跃都延续存活期
	m.rdb.Expire(m.ctx, key, memoryTTL)
}

// Clear 清空某会话的所有历史（DEL 该 key）。
func (m *RedisMemory) Clear(sessionID string) {
	m.rdb.Del(m.ctx, sessionKey(sessionID))
}

// Truncate 超长时只保留某会话最近 maxTokens 条消息（LTRIM 截断 list 尾部）。
// 简单"丢最旧"策略；更完整的 token 级摘要压缩后续在增强版实现。
func (m *RedisMemory) Truncate(sessionID string, maxTokens int) {
	if maxTokens <= 0 {
		return
	}
	m.rdb.LTrim(m.ctx, sessionKey(sessionID), -int64(maxTokens), -1)
}
