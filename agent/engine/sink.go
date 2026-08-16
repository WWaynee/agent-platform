package engine

import "context"

// ============ 冷轨完整历史写入接口（FullHistorySink） ============
//
// 引擎不能 import storage（否则成环），因此这里定义最小注入接口，
// 由启动装配（cmd/api/main.go）用 adapter 把 storage.AppendChatMessage 接进来。
// 目的是让"对话完整历史落冷轨（MySQL，永不压缩）"成为引擎可选的旁路能力，
// 不注入（sink 为 nil）则整条冷轨链路静默跳过，不影响热轨对话。

// FullHistorySink 冷轨完整历史落库的注入接口。
// Append 写入一条对话完整原文（含工具调用指令 + 执行结果），内部负责落库策略。
type FullHistorySink interface {
	Append(ctx context.Context, m ChatMsg) error
}

// ChatMsg 一条要落冷轨的对话消息（引擎侧透传结构）。
// 字段类型跟随系统现有设计：
//   - TenantID / UserID                 uint64（与 AgentContext 一致）
//   - SessionID                         string（跟随 AgentContext.SessionID，纯数字字符串，对应 sessions.id）
//   - Role / Kind / Content             string
// 不做 TraceID 字段：trace_id 一律从传入 ctx 取（全链路经 AgentContext.ToContext 已种入）。
type ChatMsg struct {
	TenantID  uint64
	UserID    uint64
	SessionID string
	Role      string
	Kind      string
	Content   string
}
