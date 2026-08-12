package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"agent-platform/config"
	"agent-platform/storage"
	"agent-platform/storage/model"
)

// ============ Service 层：业务层 ============
//
// 负责业务逻辑、事务、多 storage 组合调用。
// 不直接处理 HTTP 请求参数，由 handler 层传入。
// 不直接写 SQL，通过 storage 层访问数据库。

// CreateTenant 创建租户
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// 业务逻辑：创建租户 → 自动创建该租户首个管理员账号（role=admin）→ 初始化默认工具配置。
//
// 租户首个用户默认是管理员：租户建好后，系统自动以 adminUsername/adminPassword
// 建一个 role=admin 的用户，作为该租户的管理员入口。普通自助注册则一律是 member。
func CreateTenant(ctx context.Context, name, adminUsername, adminPassword string) (*model.Tenant, error) {
	// admin 账号默认值兜底（调用方未传则用 admin / admin123）
	if strings.TrimSpace(adminUsername) == "" {
		adminUsername = "admin"
	}
	if strings.TrimSpace(adminPassword) == "" {
		adminPassword = "admin123"
	}

	tenant := &model.Tenant{
		Name:   name,
		Status: 1, // 默认启用
		// 新租户默认 token 配额（来自配置；0 表示不限制）
		QuotaLlmToken: config.GlobalConfig.Quota.DefaultMonthlyToken,
	}
	if err := storage.CreateTenant(ctx, tenant); err != nil {
		return nil, fmt.Errorf("创建租户失败: %w", err)
	}

	// 自动创建租户首个管理员账号（传 role=admin 创建管理员）
	if _, err := Register(ctx, tenant.ID, adminUsername, adminPassword, "admin"); err != nil {
		return tenant, fmt.Errorf("租户已创建，但创建管理员账号失败: %w", err)
	}

	// 初始化该租户的默认工具配置（默认都开启）
	if err := storage.InitDefaultToolConfigs(ctx, tenant.ID); err != nil {
		return tenant, fmt.Errorf("租户已创建，但初始化默认工具配置失败: %w", err)
	}

	return tenant, nil
}

// GetTenantDetail 查询租户详情
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
func GetTenantDetail(ctx context.Context, id uint64) (*model.Tenant, error) {
	tenant, err := storage.GetTenantByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return tenant, nil
}

// ListTenants 分页查询租户列表
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
func ListTenants(ctx context.Context, page, pageSize int) ([]model.Tenant, int64, error) {
	return storage.ListTenants(ctx, page, pageSize)
}

// UpdateTenantStatus 更新租户状态（0 禁用 / 1 启用）
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// 业务校验：先确认租户存在，不存在则返回明确错误，避免更新空记录
func UpdateTenantStatus(ctx context.Context, id uint64, status int8) error {
	// 先查租户是否存在
	_, err := storage.GetTenantByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("租户不存在: id=%d", id)
		}
		return fmt.Errorf("查询租户失败: %w", err)
	}

	// 存在则更新状态
	if err := storage.UpdateTenantStatus(ctx, id, status); err != nil {
		return fmt.Errorf("更新租户状态失败: %w", err)
	}
	return nil
}
