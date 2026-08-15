package storage

import (
	"context"

	"agent-platform/storage/model"

	"gorm.io/gorm"
)

// ============ Storage 层：用户数据操作 ============
// 纯 CRUD，不写业务逻辑。
// 每个方法都接收 ctx：把请求级 trace_id/tenant_id 透传给 GORM（DB.WithContext(ctx)）。

// CreateUser 插入一条用户记录
func CreateUser(ctx context.Context, user *model.User) error {
	return DB.WithContext(ctx).Create(user).Error
}

// CreateUserTx 在调用方显式事务内插入一条用户记录。
// tx 由上层 service 用 storage.DB.Transaction 包裹传入，保证与其它子步骤同事务、原子提交。
func CreateUserTx(ctx context.Context, tx *gorm.DB, user *model.User) error {
	return tx.WithContext(ctx).Create(user).Error
}

// GetUserByUsername 按租户 + 用户名查询用户
// 参数 tenantID + username 联合查询，对应 users 表 uniqueIndex:idx_tenant_user 联合唯一索引
// 同一用户名在不同租户下互不冲突（如租户 A 和租户 B 可以都有 admin）
func GetUserByUsername(ctx context.Context, tenantID uint64, username string) (*model.User, error) {
	var user model.User
	err := DB.WithContext(ctx).Where("tenant_id = ? AND username = ?", tenantID, username).
		First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByID 按主键 ID 查询用户
func GetUserByID(ctx context.Context, id uint64) (*model.User, error) {
	var user model.User
	err := DB.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}
