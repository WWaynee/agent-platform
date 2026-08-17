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
// ⚠️ 自需求单 0004 起，注册已加「应用层全局唯一」约束（不允许新增跨租户同名用户）；
//    但 DB 层未加 username 全局唯一索引且历史数据可能仍存在跨租户同名（仅登录时按"唯一即登录/同名报错"处理）。
//    本方法仍按租户精确定位，供老调用（登录固定传 tenant_id）使用。
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

// FindUsersByUsername 按用户名全库查询（需求单 0004：纯用户名登录用）。
// 同一用户名在不同租户下可共存（users.idx_tenant_user 联合唯一），
// 故可能命中多个用户；由 service 层判断唯一性并决定登录或报"同名冲突"。
// 按 tenant_id 升序返回，保证"多租户同名"时结果顺序稳定。
func FindUsersByUsername(ctx context.Context, username string) ([]model.User, error) {
	var users []model.User
	err := DB.WithContext(ctx).Where("username = ?", username).Order("tenant_id ASC").Find(&users).Error
	if err != nil {
		return nil, err
	}
	return users, nil
}
