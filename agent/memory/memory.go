package memory

// ============ 消息类型 ============

// Role 表示一条消息的角色。
type Role string

const (
	// RoleUser 用户消息。
	RoleUser Role = "user"
	// RoleAssistant 助手（Agent）消息。
	RoleAssistant Role = "assistant"
	// RoleTool 工具（执行结果反馈）消息。
	RoleTool Role = "tool"
	// RoleSystem 系统提示消息。
	RoleSystem Role = "system"
)

// ChatMessage 一条对话消息。
// Memory 的存取以 ChatMessage 为基本单位，供 ReAct 引擎把历史拼给 LLM。
type ChatMessage struct {
	// Role 消息角色（user / assistant / tool / system）。
	Role Role
	// Content 消息内容。
	Content string
}

// ============ 记忆接口 ============

// 为什么定义成接口：
//   - 先提供内存版实现跑通流程（简单快速）；
//   - 后面换成 Redis 版，业务代码不用改——面向接口编程、方便切换实现。
//
// 多租户隔离：所有方法都显式要求 tenantID——即使 session_id 撞车，不同租户
// 的历史也天然隔离（Redis 的 key、内存的 map 都按（tenantID, sessionID）分桶）。
type Memory interface {
	// GetHistory 获取某租户某会话的完整历史消息。
	// 返回该会话下的消息列表（按时间正序），无历史时返回 nil 或空切片。
	GetHistory(tenantID uint64, sessionID string) []ChatMessage

	// AddMessage 向某租户某会话追加一条消息。
	AddMessage(tenantID uint64, sessionID string, msg ChatMessage)

	// Clear 清空某租户某会话的所有历史。
	Clear(tenantID uint64, sessionID string)

	// Truncate 超长时对某租户某会话历史做截断/摘要。
	// maxTokens 是允许保留的最大 token 数。当前先预留接口，具体策略后续实现。
	Truncate(tenantID uint64, sessionID string, maxTokens int)
}
