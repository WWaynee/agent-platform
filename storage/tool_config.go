package storage

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"agent-platform/storage/model"
)

// ============ Storage 层：租户工具权限配置 ============
//
// 只跟 tenant_tool_config 表打交道，纯 CRUD。
// 每个方法都接收 ctx：把请求级 trace_id/tenant_id 透传给 GORM（DB.WithContext(ctx)）。
// 供上层（ToolManager 的 PermissionChecker）判断"某租户是否开启某工具"。
//
// "查不到配置默认启用"采用双保险，兼顾新老租户：
//   1) 新租户创建时经 InitDefaultToolConfigs 显式写入默认开启记录；
//   2) 老租户（历史数据未初始化）或尚未配置的工具，由上层 DBPermissionChecker
//      直接放行（查不到即视为默认启用，不强制写库）。
//
// ⚠️ 因此 GetToolConfig 查不到仍返回 gorm.ErrRecordNotFound，由上层决定如何兜底。
//    上层 DBPermissionChecker 的决策是"查不到即默认放行"（详见
//    api/service/tool_permission.go）：数据库没有该租户对该工具的启用记录时，
//    视为默认启用。刻意不在此处强行返回"启用"，从而把判断口径集中在权限层一处。

// DefaultTools 新建租户时默认开启的工具集合。
// 用于 InitDefaultToolConfigs：新租户创建时把这些默认工具显式写为开启记录，
// 便于管理员在管理端看到并管理这些工具的开关状态。
var DefaultTools = []string{
	"knowledge_retrieve",
}

// GetToolConfig 查询某租户对某工具"是否开启"的配置。
// 所有查询强制 tenant_id 过滤，杜绝跨租户读取。
// 查不到对应记录时返回 gorm.ErrRecordNotFound（上层据此按"默认开启"兜底）。
func GetToolConfig(ctx context.Context, tenantID uint64, toolName string) (*model.TenantToolConfig, error) {
	var cfg model.TenantToolConfig
	err := DB.WithContext(ctx).Where("tenant_id = ? AND tool_name = ?", tenantID, toolName).
		First(&cfg).Error
	if err != nil {
		// 返回原错误，由上层通过 errors.Is(err, gorm.ErrRecordNotFound) 判断
		return nil, err
	}
	return &cfg, nil
}

// ListToolConfigs 列出某租户的全部工具配置。
// 强制 tenant_id 过滤，只返回当前租户的配置。
// 未初始化的老租户返回空列表（非错误），由上层决定展示兜底。
func ListToolConfigs(ctx context.Context, tenantID uint64) ([]model.TenantToolConfig, error) {
	var cfgs []model.TenantToolConfig
	err := DB.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("id ASC").Find(&cfgs).Error
	if err != nil {
		return nil, err
	}
	return cfgs, nil
}

// UpdateToolConfig 更新某租户对某工具是否开启。
// 采用"存在即更新、不存在即创建"的 upsert 语义（以 tenant_id+tool_name 定位），
// 支持开启（IsEnable=true）与关闭（IsEnable=false）两种状态。
// 所有查询/写入强制 tenant_id 过滤。
func UpdateToolConfig(ctx context.Context, tenantID uint64, toolName string, isEnable bool) error {
	var cfg model.TenantToolConfig
	// 尝试按 (tenant_id, tool_name) 找已有记录
	err := DB.WithContext(ctx).Where("tenant_id = ? AND tool_name = ?", tenantID, toolName).
		First(&cfg).Error
	if err == gorm.ErrRecordNotFound {
		// 不存在 → 创建
		rec := model.TenantToolConfig{
			TenantID: tenantID,
			ToolName: toolName,
			IsEnable: isEnable,
		}
		return DB.WithContext(ctx).Create(&rec).Error
	}
	if err != nil {
		return err
	}
	// 存在 → 更新 IsEnable
	return DB.WithContext(ctx).Model(&cfg).
		Where("id = ?", cfg.ID).
		Update("is_enable", isEnable).Error
}

// EnableTool 便捷方法：开启某租户的某工具。
func EnableTool(ctx context.Context, tenantID uint64, toolName string) error {
	return UpdateToolConfig(ctx, tenantID, toolName, true)
}

// DisableTool 便捷方法：关闭某租户的某工具。
func DisableTool(ctx context.Context, tenantID uint64, toolName string) error {
	return UpdateToolConfig(ctx, tenantID, toolName, false)
}

// InitDefaultToolConfigs 新租户创建时初始化默认工具配置（默认都开启）。
// 对 DefaultTools 中的每个工具：若该租户尚无配置则写为开启；已有则不覆盖
// （管理员可能已自定义），保证幂等。
func InitDefaultToolConfigs(ctx context.Context, tenantID uint64) error {
	return initDefaultToolConfigsWithDB(ctx, DB, tenantID)
}

// InitDefaultToolConfigsTx 在调用方显式事务内初始化默认工具配置（供注册租户的事务使用）。
// tx 由上层 service 用 storage.DB.Transaction 包裹传入，保证与其它子步骤同事务、原子提交。
func InitDefaultToolConfigsTx(ctx context.Context, tx *gorm.DB, tenantID uint64) error {
	return initDefaultToolConfigsWithDB(ctx, tx, tenantID)
}

// initDefaultToolConfigsWithDB 以指定的 *gorm.DB（默认连接或事务句柄）完成默认工具配置初始化。
// 对 DefaultTools 中的每个工具：若该租户尚无配置则写为开启；已有则不覆盖，保证幂等。
func initDefaultToolConfigsWithDB(ctx context.Context, db *gorm.DB, tenantID uint64) error {
	for _, name := range DefaultTools {
		var cfg model.TenantToolConfig
		err := db.WithContext(ctx).Where("tenant_id = ? AND tool_name = ?", tenantID, name).
			First(&cfg).Error
		if err == nil {
			// 已有配置（可能被自定义），跳过，不覆盖
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			// 数据库异常，中断，避免初始化不完整
			return err
		}
		// 查不到 → 默认开启（直接创建）
		rec := model.TenantToolConfig{
			TenantID: tenantID,
			ToolName: name,
			IsEnable: true,
		}
		if err := db.WithContext(ctx).Create(&rec).Error; err != nil {
			return err
		}
	}
	return nil
}
