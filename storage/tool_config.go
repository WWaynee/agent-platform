package storage

import (
	"gorm.io/gorm"

	"agent-platform/storage/model"
)

// ============ Storage 层：租户工具权限配置 ============
//
// 只跟 tenant_tool_config 表打交道，纯 CRUD。
// 供上层（ToolManager 的 PermissionChecker）判断"某租户是否开启某工具"。

// GetToolConfig 查询某租户对某工具"是否开启"的配置。
// 查不到对应记录时返回 gorm.ErrRecordNotFound（上层据此按"默认开启"兜底）。
func GetToolConfig(tenantID uint64, toolName string) (*model.TenantToolConfig, error) {
	var cfg model.TenantToolConfig
	err := DB.Where("tenant_id = ? AND tool_name = ?", tenantID, toolName).
		First(&cfg).Error
	if err != nil {
		// 返回原错误，由上层通过 errors.Is(err, gorm.ErrRecordNotFound) 判断
		return nil, err
	}
	return &cfg, nil
}

// SetToolConfig 设置某租户对某工具是否开启。
// 采用"存在即更新、不存在即创建"的 upsert 语义（以 tenant_id+tool_name 定位），
// 支持开启（IsEnable=true）与关闭（IsEnable=false）两种状态。
func SetToolConfig(tenantID uint64, toolName string, isEnable bool) error {
	var cfg model.TenantToolConfig
	// 尝试按 (tenant_id, tool_name) 找已有记录
	err := DB.Where("tenant_id = ? AND tool_name = ?", tenantID, toolName).
		First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		// 不存在 → 创建
		rec := model.TenantToolConfig{
			TenantID: tenantID,
			ToolName: toolName,
			IsEnable: isEnable,
		}
		return DB.Create(&rec).Error
	}
	if err != nil {
		return err
	}
	// 存在 → 更新 IsEnable
	return DB.Model(&cfg).
		Where("id = ?", cfg.ID).
		Update("is_enable", isEnable).Error
}

// EnableTool 便捷方法：开启某租户的某工具。
func EnableTool(tenantID uint64, toolName string) error {
	return SetToolConfig(tenantID, toolName, true)
}

// DisableTool 便捷方法：关闭某租户的某工具。
func DisableTool(tenantID uint64, toolName string) error {
	return SetToolConfig(tenantID, toolName, false)
}
