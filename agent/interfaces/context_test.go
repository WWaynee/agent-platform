package interfaces

import (
	"context"
	"testing"
)

// TestWithTenantUser_RoundTrip 验证 WithTenantUser 写入后，能通过读取函数取回。
func TestWithTenantUser_RoundTrip(t *testing.T) {
	ctx := WithTenantUser(context.Background(), 100, 200)

	if got := TenantIDFromCtx(ctx); got != 100 {
		t.Errorf("TenantIDFromCtx = %d, want 100", got)
	}
	if got := UserIDFromCtx(ctx); got != 200 {
		t.Errorf("UserIDFromCtx = %d, want 200", got)
	}
}

// TestFromCtx_NoValue 未写入时读取应返回 0（不 panic）。
func TestFromCtx_NoValue(t *testing.T) {
	ctx := context.Background()
	if got := TenantIDFromCtx(ctx); got != 0 {
		t.Errorf("TenantIDFromCtx(empty) = %d, want 0", got)
	}
	if got := UserIDFromCtx(ctx); got != 0 {
		t.Errorf("UserIDFromCtx(empty) = %d, want 0", got)
	}
}

// TestWithTenantUser_DoesNotMutateOriginal 应基于原 ctx 衍生新 ctx，不改坏原始 ctx。
func TestWithTenantUser_DoesNotMutateOriginal(t *testing.T) {
	orig := context.Background()
	_ = WithTenantUser(orig, 7, 8)
	// 原 ctx 仍是空的
	if got := TenantIDFromCtx(orig); got != 0 {
		t.Errorf("原始 ctx 不应被污染，TenantIDFromCtx = %d", got)
	}
}

// TestWithRuntimeContext_DoesNotPolluteOriginal 回归测试（P0 修复）：
// WithRuntimeContext 注入带 deadline/cancel 的运行时 ctx 时，只能作用于返回的副本，
// 不得污染调用方持有的原 AgentContext——否则后续 ToContext(nil) 会复用已被 cancel 的工具 ctx，
// 导致下一轮 LLM 调用立即超时、异步冷轨写库 context canceled（历史丢失）。
func TestWithRuntimeContext_DoesNotPolluteOriginal(t *testing.T) {
	base := &AgentContext{TenantID: 5, UserID: 6, SessionID: "7"}

	// 模拟工具执行：注入一个会立即 cancel 的运行时 ctx
	toolCtx, cancel := context.WithCancel(context.Background())
	cancel() // 工具返回即 cancel
	derived := base.WithRuntimeContext(toolCtx)

	// 1. 派生出的副本能感知该运行时 ctx（工具内 ToContext 可拿到带 cancel 的 ctx）
	dCtx := derived.ToContext(nil)
	if dCtx.Err() != context.Canceled {
		t.Errorf("副本 ToContext(nil) 应携带被 cancel 的运行时 ctx, got err=%v", dCtx.Err())
	}

	// 2. 原 AgentContext 未被污染：其 ToContext(nil) 应得到干净的 Background（rctx 为空）
	oCtx := base.ToContext(nil)
	if oCtx.Err() != nil {
		t.Errorf("原 AgentContext 被污染：ToContext(nil) err=%v（应为 nil，即未复用工具 cancel ctx）", oCtx.Err())
	}
	if got := TenantIDFromCtx(oCtx); got != 5 {
		t.Errorf("原 ctx 仍应保留 tenant_id=5, got %d", got)
	}
}
