package service

import (
	"context"

	"go.uber.org/zap"

	"agent-platform/agent/interfaces"
	"agent-platform/observability"
	"agent-platform/storage"
	"agent-platform/storage/model"
)

// ============ 审计日志（简化版） ============
//
// RecordAuditLog 记录一条审计日志到 audit_logs 表。
// 统一从 ctx 提取 tenant_id / user_id / trace_id（缺失则留空），
// 随结构化日志自动携带 trace_id，便于按链路回溯「谁在哪个请求做了哪个操作」。
//
// 审计是"尽力而为"：写入失败只打 warn 日志，**绝不阻断主业务**（审计不能拖垮操作）。
// 调用方在关键操作成功后调用即可，如：上传文档、删除文档、修改工具配置、登录。
func RecordAuditLog(ctx context.Context, operation, content string) {
	entry := &model.AuditLog{
		TenantID:  interfaces.TenantIDFromCtx(ctx),
		UserID:    interfaces.UserIDFromCtx(ctx),
		Operation: operation,
		TraceID:   interfaces.TraceIDFromCtx(ctx),
		Content:   content,
	}

	if err := storage.CreateAuditLog(ctx, entry); err != nil {
		// 审计失败不阻断业务：仅记录 warn，并带上下文与 trace_id 便于排查。
		logger := observability.WithContext(ctx)
		logger.Warn("审计日志写入失败", zap.Error(err))
	}
}
