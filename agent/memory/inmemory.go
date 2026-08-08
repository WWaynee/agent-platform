package memory

import "sync"

// InMemoryMemory 是 Memory 接口的纯内存实现。
// 内部用 map 存：key 是 sessionID，value 是该会话的消息列表。
//
// 特点：
//   - 纯内存，进程重启即丢失；
//   - 仅用于今天跑通流程 / 周六调试 Agent，下周再换 Redis 版（业务代码无需改动）。
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

// GetHistory 返回某会话的完整历史消息（按时间正序）。
// 无历史或会话不存在时返回 nil。
func (m *InMemoryMemory) GetHistory(sessionID string) []ChatMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 返回副本，防止调用方修改影响内部存储
	hist := m.sessions[sessionID]
	if len(hist) == 0 {
		return nil
	}
	out := make([]ChatMessage, len(hist))
	copy(out, hist)
	return out
}

// AddMessage 向某会话追加一条消息。
func (m *InMemoryMemory) AddMessage(sessionID string, msg ChatMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[sessionID] = append(m.sessions[sessionID], msg)
}

// Clear 清空某会话的所有历史（删除该 key）。
func (m *InMemoryMemory) Clear(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 直接删除该会话，释放内存
	delete(m.sessions, sessionID)
}

// Truncate 超长时的截断/摘要。
// 当前为预留方法：内存版只做最简单的"丢弃最旧消息、保留最近 maxTokens 条"，
// 完整策略（token 级 + 摘要压缩）后续在 Redis 版或接 memory 增强时实现。
func (m *InMemoryMemory) Truncate(sessionID string, maxTokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	hist := m.sessions[sessionID]
	if maxTokens <= 0 || len(hist) == 0 {
		return
	}
	// 简单策略：只保留最近 maxTokens 条消息
	if len(hist) > maxTokens {
		m.sessions[sessionID] = append([]ChatMessage(nil), hist[len(hist)-maxTokens:]...)
	}
}
