package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"agent-platform/agent/interfaces"
	"agent-platform/storage"
	"agent-platform/storage/model"
	"agent-platform/util"
)

// ============ Service 层：用户业务逻辑 ============

// Register 用户注册
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使 DB 日志带同一链路 ID。
// 流程：校验用户名是否已存在 → 密码 bcrypt 哈希 → 插入数据库 → 返回用户
//
// 角色规则：注册接口默认创建的是普通成员 member。只有调用方显式传 "admin" 才会创建管理员
// （例如租户创建时由系统自动给租户建首个 admin；普通注册一律 member，防止自助注册提权）。
func Register(ctx context.Context, tenantID uint64, username, password, role string) (*model.User, error) {
	// 1. 先查该租户下用户名是否已存在
	_, err := storage.GetUserByUsername(ctx, tenantID, username)
	if err == nil {
		return nil, fmt.Errorf("用户名已存在")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		// 不是"记录不存在"，是真实数据库错误
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}

	// 2. 密码哈希
	passwordHash, err := util.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("密码加密失败: %w", err)
	}

	// 3. 角色兜底：未显式指定则默认普通成员
	if role == "" {
		role = "member"
	}

	// 4. 构造用户并插入
	user := &model.User{
		TenantID:     tenantID,
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		Status:       1, // 默认启用
	}
	if err := storage.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	return user, nil
}

// Login 用户登录
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使 DB 日志带同一链路 ID。
// 登录请求需携带 tenant_id + username + password
// 无论用户不存在还是密码错误，统一返回"用户名或密码错误"（安全考虑，不让攻击者探测用户名是否有效）
func Login(ctx context.Context, tenantID uint64, username, password string) (*model.User, error) {
	// 1. 按租户 + 用户名查用户
	user, err := storage.GetUserByUsername(ctx, tenantID, username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("用户名或密码错误")
		}
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}

	// 2. 校验密码
	if !util.VerifyPassword(password, user.PasswordHash) {
		return nil, fmt.Errorf("用户名或密码错误")
	}

	// 3. 密码正确，返回用户
	// 审计：登录是公开接口，登录前 ctx 还没有 user_id（未鉴权），故用查到的 user 补齐租户/用户再记录。
	RecordAuditLog(interfaces.WithTenantUser(ctx, user.TenantID, user.ID), "登录", fmt.Sprintf("用户 %q 登录成功", user.Username))
	return user, nil
}
