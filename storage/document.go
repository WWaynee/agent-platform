package storage

import (
	"agent-platform/storage/model"
)

// ============ Storage 层：文档数据操作 ============
//
// 只跟数据库打交道，纯 CRUD，不写业务逻辑。
// 复用全局连接 DB（在 storage/mysql.go 中定义）。
//
// ⚠️ 多租户关键约束：所有查询一律带 tenant_id 租户过滤，
// 绝不允许只按 ID 查询，否则会跨租户越权访问他人文档。

// CreateDocument 插入一条文档记录
func CreateDocument(doc *model.Document) error {
	return DB.Create(doc).Error
}

// GetDocumentByID 按 ID 查询单个文档（带租户过滤）
// 查询同时满足 id 和 tenant_id，防止查到别的租户的文档
func GetDocumentByID(tenantID, id uint64) (*model.Document, error) {
	var doc model.Document
	err := DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// ListDocuments 分页查询某租户的文档列表（带租户过滤）
// 返回该页数据切片、符合条件的总条数以及可能的错误
func ListDocuments(tenantID uint64, page, pageSize int) ([]model.Document, int64, error) {
	var list []model.Document
	var total int64

	// 分页默认值兜底
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	// 统计该租户下文档总数
	if err := DB.Model(&model.Document{}).
		Where("tenant_id = ?", tenantID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 查当前页数据
	offset := (page - 1) * pageSize
	if err := DB.Model(&model.Document{}).
		Where("tenant_id = ?", tenantID).
		Offset(offset).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// DeleteDocument 软删除文档（带租户过滤）
// Document 模型带 gorm.DeletedAt，Delete 会自动转成软删除（写入 deleted_at 时间戳）
func DeleteDocument(tenantID, id uint64) error {
	return DB.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.Document{}).Error
}

// UpdateDocumentStatus 更新文档状态
// status 取值：pending / processing / success / failed
func UpdateDocumentStatus(id uint64, status string) error {
	return DB.Model(&model.Document{}).
		Where("id = ?", id).
		Update("status", status).Error
}
