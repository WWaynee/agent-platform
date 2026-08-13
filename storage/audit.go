package storage

import (
	"context"

	"agent-platform/storage/model"
)

// ============ 审计日志（audit_logs） ============
//
// CreateAuditLog 写入一条审计日志。
// ctx 携带请求级 trace_id/tenant_id，透传给 GORM 使 DB 日志带同一链路 ID。
// 审计日志为"尽力而为"写入：单条失败不影响主业务（由上层 service 决定是否告警）。
func CreateAuditLog(ctx context.Context, log *model.AuditLog) error {
	return DB.WithContext(ctx).Create(log).Error
}
