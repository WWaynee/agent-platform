package engine

// ============ Agent 上下文 ============

// AgentContext 是一次请求执行过程中贯穿各层（引擎/工具/记忆）的上下文。
// 它携带这次执行所必须的多租户与会话定位信息，并可用作通用透传容器。
type AgentContext struct {
	// TenantID 租户 ID：多租户隔离的根本依据，Agent 内所有数据访问都必须用它过滤。
	TenantID uint64
	// UserID 用户 ID：当前发起请求的用户。
	UserID uint64
	// SessionID 会话 ID：用于标识/关联一段多轮对话，由上层传入。
	SessionID string

	// 历史消息由 memory 包管理，不在该结构体内保存，这里不再预留字段。

	// meta 通用透传容器：存放其他需要沿调用链下传的信息（如 TraceID、超时、配额等）。
	// 具体键值由使用方约定，后续可按需用方法读写。
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

// ============ 请求 ============

// AgentRequest 是引擎接收到的一次用户请求。
type AgentRequest struct {
	// Query 用户的自然语言提问。
	Query string

	// 历史对话由 memory 包统一获取，请求侧不再单独携带旧消息，
	// 引擎需要时从记忆层读取。这里预留上下文引用位置，具体接线周六定。
}

// ============ 工具调用 ============

// ToolCall 记录一次对工具的实际调用（由 LLM 输出的 Action 解析而来）。
type ToolCall struct {
	// ToolName 要调用的工具名。
	ToolName string
	// Params 传给该工具的参数。用 map 便于引擎后续直接使用；
	// 若解析到的是字符串参数，也可由调用方自行转换。
	Params map[string]any
}

// ============ 返回 ============

// AgentResponse 是引擎处理完一次请求后返回的结果。
type AgentResponse struct {
	// Answer 给用户的最终回答。
	Answer string

	// ToolCalls 本次过程中实际调用过的工具列表（调试/审计用途）。
	ToolCalls []ToolCall

	// 其他元信息（如耗时、迭代轮数、中断原因等），后续按需扩展。
	meta map[string]any
}

// WithMeta 存入一条返回元信息（调试/审计扩展位）。
func (r *AgentResponse) WithMeta(key string, val any) *AgentResponse {
	if r.meta == nil {
		r.meta = make(map[string]any)
	}
	r.meta[key] = val
	return r
}

// GetMeta 读取返回元信息，不存在时返回 nil。
func (r *AgentResponse) GetMeta(key string) any {
	if r.meta == nil {
		return nil
	}
	return r.meta[key]
}
