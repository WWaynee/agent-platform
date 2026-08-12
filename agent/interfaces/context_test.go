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
