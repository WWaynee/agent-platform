package storage

import (
	"context"

	"agent-platform/storage/model"
)

// ============ Storage 层：对话消息完整历史（chat_messages，冷轨） ============
//
// 只跟数据库打交道，纯 CRUD，不写业务逻辑。
// 复用全局连接 DB（storage/mysql.go 中定义）；每个方法接收 ctx，
// 透传请求级 trace_id/tenant_id 给 GORM（DB.WithContext(ctx)），使慢查询/错误日志带同一链路 ID。
//
// ⚠️ 多租户关键约束：所有查询一律带 tenant_id 租户过滤，绝不允许只按 session_id 查询，
// 否则会跨租户越权读到他人会话的完整历史。

// AppendChatMessage 写入一条对话完整历史原文（冷轨落库）。
// 由调用方（engine 的 FullHistorySink adapter）填好各字段；TraceID 应从 ctx 取。
// 冷轨"尽力而为"：单条失败不影响主业务（由上层决定是否告警）。
func AppendChatMessage(ctx context.Context, msg *model.ChatMessage) error {
	return DB.WithContext(ctx).Create(msg).Error
}

// ListChatMessagesBySession 查询某租户某会话的完整历史原文（冷轨）。
// 带 tenant_id 强制过滤；按 created_at / id 正序返回（即对话真实时序）。
// 无可读记录时返回空切片（而非 nil），便于调用方统一处理。
func ListChatMessagesBySession(ctx context.Context, tenantID, sessionID uint64) ([]model.ChatMessage, error) {
	var list []model.ChatMessage
	err := DB.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ?", tenantID, sessionID).
		Order("created_at ASC, id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []model.ChatMessage{}
	}
	return list, nil
}
