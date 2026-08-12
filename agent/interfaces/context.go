package interfaces

import "context"

// ============ 请求过程中用于跨层透传租户/用户标识的 context 键 ============
//
// 目的：LLM 调用、工具执行等内部环节需要知道当前请求属于哪个租户/用户，
// 以便做用量统计、配额、审计等。由于这些环节通过 context.Context 传递（Go 惯例），
// 这里统一定义安全取存的键常量与便捷封装，供各层复用，避免各处手写 magic string。
//
// 使用方：
//   - agent/engine 在调用 LLM 前把租户/用户塞进 ctx（供 llmclient 的 UsageReporter 读取）；
//   - api/service 的 UsageReporter 实现从 ctx 提取租户/用户做 Redis 累加。
//
// 键值统一收敛在本包，两端（写入方/读取方）依赖同一份定义，杜绝命名不一致。

type ctxKey int

const (
	ctxKeyTenantID ctxKey = iota
	ctxKeyUserID
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
