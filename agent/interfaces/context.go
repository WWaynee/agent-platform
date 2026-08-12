package interfaces

import "context"

// ============ 请求过程中用于跨层透传租户/用户/链路ID的 context 键 ============
//
// 目的：LLM 调用、工具执行、日志记录等内部环节需要知道当前请求属于哪个租户/用户、
// 以及是哪条链路（trace_id），以便做用量统计、配额、审计、全链路日志等。
// 由于这些环节通过 context.Context 传递（Go 惯例），这里统一定义安全取存的键常量
// 与便捷封装，供各层复用，避免各处手写 magic string。
//
// 使用方：
//   - agent/engine 在调用 LLM 前把租户/用户塞进 ctx（供 llmclient 的 UsageReporter 读取）；
//   - api/service 的 UsageReporter 实现从 ctx 提取租户/用户做 Redis 累加；
//   - observability.WithContext 从 ctx 提取 trace_id / tenant_id / user_id 自动拼进日志。
//
// 键值统一收敛在本包，两端（写入方/读取方）依赖同一份定义，杜绝命名不一致。

type ctxKey int

const (
	ctxKeyTenantID ctxKey = iota
	ctxKeyUserID
	ctxKeyTraceID
)

// WithTenantUser 把租户 ID / 用户 ID 写入 ctx，返回衍生出的新 ctx。
// 供引擎在调用 LLM / 工具前调用，让用量统计等下游能从 ctx 拿到归属。
func WithTenantUser(ctx context.Context, tenantID, userID uint64) context.Context {
	ctx = context.WithValue(ctx, ctxKeyTenantID, tenantID)
	ctx = context.WithValue(ctx, ctxKeyUserID, userID)
	return ctx
}

// TenantIDFromCtx 从 ctx 读取租户 ID，未设置或类型不符时返回 0。
func TenantIDFromCtx(ctx context.Context) uint64 {
	if v, ok := ctx.Value(ctxKeyTenantID).(uint64); ok {
		return v
	}
	return 0
}

// UserIDFromCtx 从 ctx 读取用户 ID，未设置或类型不符时返回 0。
func UserIDFromCtx(ctx context.Context) uint64 {
	if v, ok := ctx.Value(ctxKeyUserID).(uint64); ok {
		return v
	}
	return 0
}

// WithTraceID 把链路 ID（trace_id）写入 ctx，返回衍生出的新 ctx。
// 供 HTTP 入口（Trace 中间件）在请求级把 trace_id 种进请求 context，
// 或内部有需要时生成/透传子链路 id。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ctxKeyTraceID, traceID)
}

// TraceIDFromCtx 从 ctx 读取链路 ID，未设置时返回空字符串。
func TraceIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyTraceID).(string); ok {
		return v
	}
	return ""
}
