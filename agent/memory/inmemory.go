package memory

import (
	"fmt"
	"sync"
)

// InMemoryMemory 是 Memory 接口的纯内存实现。
// 内部用 map 存：key 是 "租户ID:会话ID" 复合键，value 是该会话的消息列表。
//
// 特点：
//   - 纯内存，进程重启即丢失；
//   - 仅用于测试 / smoke 调试 Agent，生产用 Redis 版（业务代码无需改动）。
//
// 多租户隔离：key 带 tenantID，即使不同租户撞了同一 sessionID 也不会混存。
type InMemoryMemory struct {
	mu       sync.Mutex
	sessions map[string][]ChatMessage
}

// NewInMemoryMemory 构造一个空的内存版记忆管理器。
func NewInMemoryMemory() *InMemoryMemory {
	return &InMemoryMemory{
		sessions: make(map[string][]ChatMessage),
	}
}

// memKey 生成内存 map 的键："{tenantID}:{sessionID}"，实现按租户 + 会话隔离。
func memKey(tenantID uint64, sessionID string) string {
	return fmt.Sprintf("%d:%s", tenantID, sessionID)
}

// GetHistory 返回某租户某会话的完整历史消息（按时间正序）。
// 无历史或会话不存在时返回 nil。
func (m *InMemoryMemory) GetHistory(tenantID uint64, sessionID string) []ChatMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 返回副本，防止调用方修改影响内部存储
	hist := m.sessions[memKey(tenantID, sessionID)]
	if len(hist) == 0 {
		return nil
	}
	out := make([]ChatMessage, len(hist))
	copy(out, hist)
	return out
}

// AddMessage 向某租户某会话追加一条消息。
func (m *InMemoryMemory) AddMessage(tenantID uint64, sessionID string, msg ChatMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := memKey(tenantID, sessionID)
	m.sessions[k] = append(m.sessions[k], msg)
}

// Clear 清空某租户某会话的所有历史（删除该 key）。
func (m *InMemoryMemory) Clear(tenantID uint64, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 直接删除该会话，释放内存
	delete(m.sessions, memKey(tenantID, sessionID))
}

// Truncate 超长时的截断/摘要。
// 当前为预留方法：内存版只做最简单的"丢弃最旧消息、保留最近 maxTokens 条"，
// 完整策略（token 级 + 摘要压缩）后续在 Redis 版或接 memory 增强时实现。
func (m *InMemoryMemory) Truncate(tenantID uint64, sessionID string, maxTokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := memKey(tenantID, sessionID)
	hist := m.sessions[k]
	if maxTokens <= 0 || len(hist) == 0 {
		return
	}
	// 简单策略：只保留最近 maxTokens 条消息
	if len(hist) > maxTokens {
		m.sessions[k] = append([]ChatMessage(nil), hist[len(hist)-maxTokens:]...)
	}
}
