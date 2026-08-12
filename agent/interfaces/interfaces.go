package interfaces

import "context"

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

// 元信息中 trace_id 使用的规范 key（与 context.go 的 ctxKeyTraceID 对应，经 ToContext 写入 ctx）。
const metaKeyTraceID = "__trace_id__"

// WithTraceID 把链路 ID 记入 AgentContext 的透传元信息，返回同实例（链式可用）。
// HTTP 入口（handler.Chat）可在构造 AgentContext 后调用它，把请求级 trace_id 带入
// Agent 内部；随后 ToContext / observability.WithAgentContext 把它写进标准 ctx 供日志携带。
func (c *AgentContext) WithTraceID(traceID string) *AgentContext {
	return c.WithMeta(metaKeyTraceID, traceID)
}

// TraceID 读取 AgentContext 中记录的链路 ID；未设置时返回空字符串。
func (c *AgentContext) TraceID() string {
	if c == nil {
		return ""
	}
	if v, ok := c.GetMeta(metaKeyTraceID).(string); ok {
		return v
	}
	return ""
}

// ToContext 把 AgentContext 中的租户 / 用户 / trace_id 合并进一个标准 context.Context。
//
// 用途：Agent 内部在调用 LLM / 存储等"以标准 ctx 贯穿"的下游时，把 AgentContext 承载的
// 链路信息翻译成 context 值，使 trace_id / tenant_id / user_id 能经 observability.WithContext
// 自动进入日志。避免"新建 context 把 trace_id 弄丢"。
func (c *AgentContext) ToContext(base context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	ctx := WithTenantUser(base, c.TenantID, c.UserID)
	if tid := c.TraceID(); tid != "" {
		ctx = WithTraceID(ctx, tid)
	}
	return ctx
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
