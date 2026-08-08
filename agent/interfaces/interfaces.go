// Package interfaces 存放跨 Agent 各子包共享的公共类型。
//
// 为什么单独一个包：
//   避免 engine ↔ toolmanager 之间出现循环依赖（toolmanager 的工具需要
//   知道"执行上下文"，而 engine 又持有 toolmanager；把共享的 AgentContext
//   下沉到独立包后，两个子包都只依赖它，依赖方向变成单向、无环）。
package interfaces

// AgentContext 是一次请求执行过程中贯穿各层（引擎/工具/记忆）的上下文。
// 它携带这次执行所必须的多租户与会话定位信息，并可用作通用透传容器。
type AgentContext struct {
	// TenantID 租户 ID：多租户隔离的根本依据，Agent 内所有数据访问都必须用它过滤。
	TenantID uint64
	// UserID 用户 ID：当前发起请求的用户。
	UserID uint64
	// SessionID 会话 ID：用于标识/关联一段多轮对话，由上层传入。
	SessionID string

	// 历史消息由 memory 包管理，不在该结构体内保存。

	// meta 通用透传容器：存放其他需要沿调用链下传的信息（如 TraceID、超时、配额等）。
	meta map[string]any
}

// WithMeta 存入一条透传元信息。
func (c *AgentContext) WithMeta(key string, val any) *AgentContext {
	if c.meta == nil {
		c.meta = make(map[string]any)
	}
	c.meta[key] = val
	return c
}

// GetMeta 读取透传元信息，不存在时返回 nil。
func (c *AgentContext) GetMeta(key string) any {
	if c.meta == nil {
		return nil
	}
	return c.meta[key]
}
