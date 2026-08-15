package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"agent-platform/agent/interfaces"
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
//
// 注意：本方法被 CreateTenant handler（管理员视角建租户）与 RegisterTenant（公开注册租户）
// 共用。所有子步骤（建租户 / 建 admin / 初始化工具配置）用事务包裹，任一步失败整体回滚，
// 不留残缺租户。
func CreateTenant(ctx context.Context, name, adminUsername, adminPassword string) (*model.Tenant, error) {
	tenant, _, err := RegisterTenant(ctx, name, adminUsername, adminPassword)
	if err != nil {
		return nil, err
	}
	return tenant, nil
}

// RegisterTenant 注册租户（公开入口：注册成功同时原子创建首个 admin 与默认工具配置）
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
//
// 安全约定（对应需求单 0001）：
//   - 调用方不能指定 tenant_id（由建租户自动生成 ID）与 role（首个账号固定 role="admin"，不可提权）。
//   - 整个流程用 transactions 包裹：CreateTenant → Register(首个 admin) → InitDefaultToolConfigs，
//     任一步失败整体回滚，绝不留残缺租户。
//
// 返回建好的租户与首个管理员账号。
func RegisterTenant(ctx context.Context, name, adminUsername, adminPassword string) (*model.Tenant, *model.User, error) {
	// admin 账号默认值兜底（调用方未传则用 admin / admin123）
	if strings.TrimSpace(adminUsername) == "" {
		adminUsername = "admin"
	}
	if strings.TrimSpace(adminPassword) == "" {
		adminPassword = "admin123"
	}

	var createdTenant *model.Tenant
	var createdAdmin *model.User

	// 事务包裹：租户 + 首个 admin + 默认工具配置 原子创建，任一步失败整体回滚。
	err := storage.DB.Transaction(func(tx *gorm.DB) error {
		tenant := &model.Tenant{
			Name:   name,
			Status: 1, // 默认启用
			// 新租户默认 token 配额（来自配置；0 表示不限制）
			QuotaLlmToken: config.GlobalConfig.Quota.DefaultMonthlyToken,
		}
		if err := storage.CreateTenantTx(ctx, tx, tenant); err != nil {
			// 租户名唯一（tenants.idx_tenants_name）：重复注册同名租户 → 明确提示冲突（整体回滚）。
			if isDuplicateNameErr(err) {
				return fmt.Errorf("租户名已被注册，请更换名称")
			}
			return fmt.Errorf("创建租户失败: %w", err)
		}
		createdTenant = tenant

		// 创建租户首个管理员账号（固定 role=admin，不可由调用方指定，堵提权）。
		// registerInTx 支持事务句柄，与建租户、初始化工具配置同事务原子提交。
		admin, err := registerInTx(ctx, tx, tenant.ID, adminUsername, adminPassword, "admin")
		if err != nil {
			return fmt.Errorf("创建租户首个管理员账号失败: %w", err)
		}
		createdAdmin = admin

		// 初始化该租户的默认工具配置（默认都开启）
		if err := storage.InitDefaultToolConfigsTx(ctx, tx, tenant.ID); err != nil {
			return fmt.Errorf("初始化租户默认工具配置失败: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// 审计：注册租户成功。公开接口无当前操作者，故用新建租户 + 管理员补齐上下文字段（trace_id 沿用请求级）。
	RecordAuditLog(
		interfaces.WithTenantUser(ctx, createdTenant.ID, createdAdmin.ID),
		"注册租户",
		fmt.Sprintf("注册租户 %q，创建管理员账号 %q", createdTenant.Name, createdAdmin.Username),
	)

	return createdTenant, createdAdmin, nil
}

// isDuplicateNameErr 判断是否为 MySQL 唯一键冲突（Error 1062）。
// 用于把"租户名已存在"这类 DB 冲突转成对用户友好的错误提示（依旧由事务整体回滚，不留残缺租户）。
func isDuplicateNameErr(err error) bool {
	if err == nil {
		return false
	}
	// MySQL 唯一键冲突错误码 1062（Duplicate entry ... for key 'xxx'）。
	// 覆盖 GORM 事务包装后仍透传的驱动错误原文，避免依赖易失的 errors.As 类型断言。
	return strings.Contains(err.Error(), "Duplicate entry") &&
		strings.Contains(err.Error(), "1062")
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
