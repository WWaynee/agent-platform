package storage

import (
	"agent-platform/storage/model"
)

// ============ Storage 层：用户数据操作 ============
// 纯 CRUD，不写业务逻辑。

// CreateUser 插入一条用户记录
func CreateUser(user *model.User) error {
	return DB.Create(user).Error
}

// GetUserByUsername 按租户 + 用户名查询用户
// 参数 tenantID + username 联合查询，对应 users 表 uniqueIndex:idx_tenant_user 联合唯一索引
// 同一用户名在不同租户下互不冲突（如租户 A 和租户 B 可以都有 admin）
func GetUserByUsername(tenantID uint64, username string) (*model.User, error) {
	var user model.User
	err := DB.Where("tenant_id = ? AND username = ?", tenantID, username).
		First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByID 按主键 ID 查询用户
func GetUserByID(id uint64) (*model.User, error) {
	var user model.User
	err := DB.Where("id = ?", id).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}
