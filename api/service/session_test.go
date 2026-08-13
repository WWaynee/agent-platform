package service

import (
	"context"
	"fmt"
	"testing"

	"agent-platform/agent/interfaces"
	"agent-platform/storage"
	"agent-platform/storage/model"
)

// TestCreateSessionAudit 验证 CreateSession 在创建成功后写入「创建会话」审计日志：
//  1. operation = "创建会话"；
//  2. content 含会话标题与 ID；
//  3. tenant_id / user_id / trace_id 从 ctx 正确提取落库。
//
// 走真实 MySQL（setupTestDB 已在 audit_test.go 定义，DB 需要可连），连不上时跳过。
func TestCreateSessionAudit(t *testing.T) {
	setupTestDB(t)

	const tenantID, userID = 9001, 1234
	// 构造带租户/用户/trace 的 ctx（模拟已登录请求上下文，JWT 中间件就是这么种入的）
	ctx := interfaces.WithTenantUser(context.Background(), tenantID, userID)
	ctx = interfaces.WithTraceID(ctx, "session-audit-trace-001")

	const title = "审计校验会话"
	id, err := CreateSession(ctx, tenantID, userID, title)
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	// 清除本次会话与其审计记录（避免污染）
	defer storage.DB.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&model.Session{})

	// 查回最近的「创建会话」审计记录
	var row model.AuditLog
	if err := storage.DB.WithContext(ctx).
		Where("operation = ? AND tenant_id = ?", "创建会话", tenantID).
		Order("id DESC").
		First(&row).Error; err != nil {
		t.Fatalf("读取「创建会话」审计日志失败: %v", err)
	}
	defer storage.DB.WithContext(ctx).Delete(&row)

	if row.TenantID != tenantID {
		t.Errorf("tenant_id 应为 %d，实际 %d", tenantID, row.TenantID)
	}
	if row.UserID != userID {
		t.Errorf("user_id 应为 %d，实际 %d", userID, row.UserID)
	}
	if row.TraceID != "session-audit-trace-001" {
		t.Errorf("trace_id 应为 session-audit-trace-001，实际 %q", row.TraceID)
	}
	if row.Operation != "创建会话" {
		t.Errorf("operation 应为 创建会话，实际 %q", row.Operation)
	}
	wantContent := fmt.Sprintf("新建会话 %q（ID=%d）", title, id)
	if row.Content != wantContent {
		t.Errorf("content 应为 %q，实际 %q", wantContent, row.Content)
	}

	t.Log("✅ CreateSession 埋点：创建成功后写入「创建会话」审计（含租户/用户/trace_id）")
}
