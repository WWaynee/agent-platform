package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"agent-platform/storage"
)

// ============ Service 层：工具开关配置（租户管理端） ============
//
// 供租户管理员在管理端查看/控制本租户可用工具的开关状态。
// 数据落在 storage 层的 tenant_tool_config 表。
//
// 命名冲突说明：本文件在 service 包，storage 包也有同名 UpdateToolConfig，
// 二者不同包不冲突。本层负责业务口径（"查不到默认启用"等），storage 只做纯 CRUD。

// GetToolEnabled 查询某租户对某工具是否启用。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使 DB 日志带同一链路 ID。
// 遵循统一策略：查不到配置记录（DB 无该租户对该工具的开关记录）时，视为"默认启用"返回 true。
// 其它数据库错误原样返回，交由上层处理。
func GetToolEnabled(ctx context.Context, tenantID uint64, toolName string) (bool, error) {
	cfg, err := storage.GetToolConfig(ctx, tenantID, toolName)
	if err == nil {
		return cfg.IsEnable, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 没配置 → 默认启用（与 DBPermissionChecker 的"查不到默认放行"保持一致）
		return true, nil
	}
	return false, err
}

// UpdateToolEnabled 更新某租户对某工具是否启用（upsert）。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// 校验工具名非空；存储层按 (tenant_id, tool_name) 定位，存在即更新、不存在即创建。
func UpdateToolEnabled(ctx context.Context, tenantID uint64, toolName string, isEnable bool) error {
	if toolName == "" {
		return fmt.Errorf("工具名不能为空")
	}
	if err := storage.UpdateToolConfig(ctx, tenantID, toolName, isEnable); err != nil {
		return fmt.Errorf("更新工具配置失败: %w", err)
	}
	// 审计：记录修改工具配置行为（尽力而为，不影响主流程）。
	state := "禁用"
	if isEnable {
		state = "启用"
	}
	RecordAuditLog(ctx, "修改工具配置", fmt.Sprintf("设置工具 %q 为%s", toolName, state))
	return nil
}
