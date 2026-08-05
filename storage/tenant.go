package storage

import (
	"gorm.io/gorm"

	"agent-platform/storage/model"
)

// ============ Storage 层：数据层 ============
//
// 负责：
//   1. 只跟数据库打交道，纯 CRUD
//   2. 不写业务逻辑、不做业务判断
//   3. 供 service 层调用
//
// 这一层可复用 storage/model 中已定义的模型结构体（model.Tenant）。
// 换数据库时只需改这一层，业务层不受影响。

// DB 为全局 GORM 连接（已在 storage/mysql.go 中定义，此处复用）

// 占位文件，后续实现租户的数据操作：
//   - CreateTenant  插入租户记录
//   - ListTenants   查询租户列表（分页）
//   - GetTenantByID 根据 ID 查询租户
//   - UpdateStatus  更新租户状态

func getDB() *gorm.DB {
	return DB
}

// 以下为后续实现的占位说明
var _ = model.Tenant{} // 引用模型，确保模型包被使用
