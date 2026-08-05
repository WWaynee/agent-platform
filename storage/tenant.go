package storage

import (
	"agent-platform/storage/model"
)

// ============ Storage 层：数据层 ============
//
// 只跟数据库打交道，纯 CRUD，不写业务逻辑、不做业务判断。
// 复用全局连接 DB（在 storage/mysql.go 中定义）。
// 换数据库时只需改这一层，业务层不受影响。

// CreateTenant 插入一条租户记录
func CreateTenant(tenant *model.Tenant) error {
	return DB.Create(tenant).Error
}

// GetTenantByID 根据主键 ID 查询单个租户
// 返回 nil（记录不存在）时，err 为 gorm.ErrRecordNotFound
func GetTenantByID(id uint64) (*model.Tenant, error) {
	var tenant model.Tenant
	err := DB.Where("id = ?", id).First(&tenant).Error
	if err != nil {
		return nil, err
	}
	return &tenant, nil
}

// ListTenants 分页查询租户列表
// 返回该页的数据切片、符合条件的总条数，以及可能的错误
func ListTenants(page, pageSize int) ([]model.Tenant, int64, error) {
	var list []model.Tenant
	var total int64

	// 分页默认值兜底（避免负数或 0 导致查询异常）
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	// 先统计总条数
	if err := DB.Model(&model.Tenant{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 再查当前页数据
	offset := (page - 1) * pageSize
	if err := DB.Model(&model.Tenant{}).
		Offset(offset).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// UpdateTenantStatus 更新租户状态（0 禁用 / 1 启用）
// 使用 Updates + map 方式，确保 status 为零值（0）时也能正确更新
func UpdateTenantStatus(id uint64, status int8) error {
	return DB.Model(&model.Tenant{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{"status": status}).Error
}
