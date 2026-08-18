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

	// rctx 运行时标准上下文：可选承载 deadline / cancellation（如工具执行超时）。
	// 为 nil 时按原逻辑在 ToContext 里新建；工具可通过 ctx.ToContext(nil) 拿到带
	// deadline 的 ctx 感知超时，避免因 AgentContext 不承载标准 ctx 而无法实现工具超时控制。
	rctx context.Context
}

// WithRuntimeContext 注入一个"运行时标准 ctx"，可承载 deadline / cancellation。
// 后续调用 ToContext 时会优先以它为基准（而非新建空白 ctx），使超时/取消信号能沿
// LLM/工具等下游调用链下传。
//
// ⚠️ **返回 AgentContext 副本而非修改原对象**（2026-08 修复）：原先链式返回同实例会把传入的
//    运行时 ctx（常为「带 ToolTimeout deadline 的 ctx」）**永久污染到调用方持有的原 AgentContext
//    （rctx 字段）**，导致后续 `ToContext(nil)` 复用该带 deadline/cancel 的 ctx——工具返回后
//    cancel() 随之触发，使同一会话下一轮的 LLM 调用立即 `context deadline exceeded`（表现为
//    "模型服务暂时不可用"），并使异步冷轨写库全部 `context canceled`（历史丢失）。
//    改为返回副本后，运行时 ctx 只作用于本次返回的副本（工具内可感知超时），不污染原 ctx。
func (c *AgentContext) WithRuntimeContext(ctx context.Context) *AgentContext {
	out := c.copyAsValue()
	if ctx != nil {
		out.rctx = ctx
	}
	return &out
}

// copyAsValue 返回 AgentContext 的字段副本（值）。用于需要"局部携带某个运行时 ctx
// 而不污染原对象"的场景（如 WithRuntimeContext 注入工具超时 ctx 时只作用于派生的副本）。
func (c *AgentContext) copyAsValue() AgentContext {
	if c == nil {
		return AgentContext{}
	}
	out := *c
	return out
}

// RuntimeContext 返回此前注入的运行时标准 ctx；未注入时返回 nil。
// 工具/引擎内部如需读取 deadline 或监听取消，可基于它派生：
//
//	rctx := ctx.RuntimeContext()
//	if rctx == nil { /* 无超时约束 */ }
func (c *AgentContext) RuntimeContext() context.Context {
	if c == nil {
		return nil
	}
	return c.rctx
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
	// 优先以注入的运行时 ctx 为基准（可携带 deadline/cancellation，如工具执行超时），
	// 保留其超时语义；未注入时退回默认（新背景 ctx 或调用方传入的 base）。
	if c.rctx != nil {
		base = c.rctx
	} else if base == nil {
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
