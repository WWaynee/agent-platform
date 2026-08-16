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

// TestGetSessionMessagesReadsMySQL 验证冷轨完整历史落库/读回：
//  1. 创建会话后，直接经 storage.AppendChatMessage 写入 question/tool_call/tool_result/answer；
//  2. storage.ListChatMessagesBySession 能按 created_at/id 正序读回，字段正确；
//  3. GetSessionMessages 改读 MySQL，返回 {role, content, kind}，kind 正确区分；
//  4. 多租户隔离：租户 B 用相同 session_id 查不到租户 A 写入的完整历史。
//
// 走真实 MySQL（setupTestDB，缺少 .env/连不上时跳过）。
func TestGetSessionMessagesReadsMySQL(t *testing.T) {
	setupTestDB(t)

	const tenantID, userID = 9101, 5678
	ctx := interfaces.WithTenantUser(context.Background(), tenantID, userID)
	ctx = interfaces.WithTraceID(ctx, "session-msg-trace-001")

	// 确保冷轨表存在（新表，生产迁移可能未跑；自动建表避免测试因缺表失败）
	if err := storage.DB.AutoMigrate(&model.ChatMessage{}); err != nil {
		t.Fatalf("AutoMigrate chat_messages 失败: %v", err)
	}

	// 1. 建会话，拿到 session_id
	id, err := CreateSession(ctx, tenantID, userID, "冷轨完整历史测试")
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	defer storage.DB.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&model.Session{})

	// 2. 写入完整历史（含工具调用指令 + 结果），模拟一轮含工具调用的对话
	traces := []struct {
		role, kind, content string
	}{
		{"user", "question", "今年销售额是多少？"},
		{"tool", "tool_call", "knowledge_retrieve query=销售额"},
		{"tool", "tool_result", "检索命中：2024 年销售额 1200 万。"},
		{"assistant", "answer", "根据检索结果，2024 年销售额为 1200 万。"},
	}
	for _, tr := range traces {
		msg := &model.ChatMessage{
			TenantID:  tenantID,
			UserID:    userID,
			SessionID: id,
			Role:      tr.role,
			Kind:      tr.kind,
			Content:   tr.content,
			TraceID:   "session-msg-trace-001",
		}
		if err := storage.AppendChatMessage(ctx, msg); err != nil {
			t.Fatalf("AppendChatMessage 失败: %v", err)
		}
		defer storage.DB.WithContext(ctx).Delete(msg)
	}

	// 3. storage 层读回：正序 + 字段正确
	list, err := storage.ListChatMessagesBySession(ctx, tenantID, id)
	if err != nil {
		t.Fatalf("ListChatMessagesBySession 失败: %v", err)
	}
	if len(list) != len(traces) {
		t.Fatalf("期望 %d 条记录，实际 %d", len(traces), len(list))
	}
	for i, m := range list {
		if m.Role != traces[i].role || m.Kind != traces[i].kind || m.Content != traces[i].content {
			t.Errorf("第 %d 条不匹配：got(%s,%s,%q) want(%s,%s,%q)",
				i, m.Role, m.Kind, m.Content, traces[i].role, traces[i].kind, traces[i].content)
		}
	}
	if list[0].Kind != "question" { // 正序：第一条应为提问
		t.Errorf("正序首条 kind 应为 question，实际 %q", list[0].Kind)
	}

	// 4. GetSessionMessages 改读 MySQL，返回 {role, content, kind}
	msgs, err := GetSessionMessages(ctx, tenantID, userID, id)
	if err != nil {
		t.Fatalf("GetSessionMessages 失败: %v", err)
	}
	if len(msgs) != len(traces) {
		t.Fatalf("GetSessionMessages 期望 %d 条，实际 %d", len(traces), len(msgs))
	}
	if msgs[1]["kind"] != "tool_call" || msgs[2]["kind"] != "tool_result" {
		t.Errorf("kind 未正确区分工具指令/结果：got %v / %v", msgs[1]["kind"], msgs[2]["kind"])
	}
	if msgs[0]["role"] != "user" || msgs[3]["role"] != "assistant" {
		t.Errorf("role 未正确返回：%v", msgs)
	}

	// 5. 多租户隔离：租户 B 用相同 session_id 查不到
	ctxB := interfaces.WithTenantUser(context.Background(), 9202, 999)
	listB, err := storage.ListChatMessagesBySession(ctxB, 9202, id)
	if err != nil {
		t.Fatalf("租户B查询失败: %v", err)
	}
	if len(listB) != 0 {
		t.Errorf("多租户隔离失败：租户B应查不到 %d 条，实际 %d 条", len(traces), len(listB))
	}

	t.Log("✅ 冷轨完整历史：落库/读回/正序/kind/多租户隔离 均通过")
}

// TestChatMessageEmptyReturnsEmptySlice 验证无记录时返回空切片而非 nil，调用方好处理。
func TestChatMessageEmptyReturnsEmptySlice(t *testing.T) {
	setupTestDB(t)
	if err := storage.DB.AutoMigrate(&model.ChatMessage{}); err != nil {
		t.Fatalf("AutoMigrate chat_messages 失败: %v", err)
	}
	ctx := context.Background()
	list, err := storage.ListChatMessagesBySession(ctx, 1, 999999)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if list == nil {
		t.Fatalf("无记录时应返回空切片而非 nil")
	}
}
