package storage

import (
	"agent-platform/storage/model"
)

// ============ Storage 层：会话数据操作 ============
//
// 只跟数据库打交道，纯 CRUD，不写业务逻辑、不做业务判断。
// 复用全局连接 DB（在 storage/mysql.go 中定义）。
//
// 会话元数据（标题/创建时间等）落 MySQL（session 表）；
// 会话的对话消息内容仍走 Redis（session:{tenant_id}:{session_id}:messages）。
//
// ⚠️ 多租户关键约束：所有查询一律带 tenant_id 租户过滤，
// 绝不允许只按 ID 查询，否则会跨租户越权访问他人会话。

// CreateSession 插入一条会话记录。
// 由调用方（service 层）填好 TenantID / UserID / Title。
func CreateSession(session *model.Session) error {
	return DB.Create(session).Error
}

// GetSessionByID 按 ID 查询单个会话（带租户过滤）。
// 查询同时满足 id 和 tenant_id，防止查到别的租户的会话。
// 记录不存在时返回 nil，err 为 gorm.ErrRecordNotFound。
func GetSessionByID(tenantID, id uint64) (*model.Session, error) {
	var s model.Session
	err := DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSessions 分页查询某租户某用户的会话列表（带租户 + 用户过滤）。
// 返回该页数据切片、符合条件的总条数以及可能的错误。
// userID 用于"只能看到自己的会话"；若需查租户全量，可传 userID=0。
func ListSessions(tenantID, userID uint64, page, pageSize int) ([]model.Session, int64, error) {
	var list []model.Session
	var total int64

	// 分页默认值兜底（避免负数或 0 导致查询异常）
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	// 租户过滤是底线；userID>0 时再加用户过滤
	query := DB.Model(&model.Session{}).Where("tenant_id = ?", tenantID)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}

	// 统计符合条件的会话总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 查当前页数据，按更新时间倒序（最近更新的会话在前）
	offset := (page - 1) * pageSize
	if err := query.
		Order("updated_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// DeleteSession 软删除会话（带租户过滤）。
// Session 模型带 gorm.DeletedAt，Delete 会自动转成软删除（写入 deleted_at 时间戳）。
// 只按 id + tenant_id 删除，防止跨租户误删他人会话。
func DeleteSession(tenantID, id uint64) error {
	return DB.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.Session{}).Error
}
