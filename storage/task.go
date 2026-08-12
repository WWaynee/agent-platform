package storage

import (
	"context"

	"agent-platform/storage/model"
)

// ============ Storage 层：异步任务数据操作 ============
//
// 只跟数据库打交道，纯 CRUD，不写业务逻辑、不做业务判断。
// 复用全局连接 DB（在 storage/mysql.go 中定义）。
// 每个方法都接收 ctx：把请求级 trace_id/tenant_id 透传给 GORM（DB.WithContext(ctx)）。
//
// ⚠️ 多租户关键约束：所有查询一律带 tenant_id 租户过滤，
// 绝不允许只按 ID 查询，否则会跨租户越权访问他人任务。

// CreateTask 插入一条异步任务记录。
// 由调用方（service 层）填好 TenantID / TaskType / BizID / Status 等。
func CreateTask(ctx context.Context, task *model.AgentTask) error {
	return DB.WithContext(ctx).Create(task).Error
}

// UpdateTaskStatus 更新任务状态与错误信息。
// 带租户过滤，防止误改他人任务的状态。
//
// 参数：
//   - id：任务 ID
//   - status：目标状态（pending/processing/success/failed）
//   - errMsg：错误信息（一般失败时记录，成功时传空串清空）
func UpdateTaskStatus(ctx context.Context, id uint64, status string, errMsg string) error {
	return DB.WithContext(ctx).Model(&model.AgentTask{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":    status,
			"error_msg": errMsg,
		}).Error
}

// UpdateTaskRetry 更新任务的重试次数、状态与错误信息（用于消费失败重试场景）。
// 相比 UpdateTaskStatus 多递增 retry_count。
//
// 参数：
//   - id：任务 ID
//   - retryCount：累计重试次数
//   - status：目标状态（重试期间的临时状态，如 processing/pending）
//   - errMsg：本次失败的错误信息（供排查）
func UpdateTaskRetry(ctx context.Context, id uint64, retryCount int, status string, errMsg string) error {
	return DB.WithContext(ctx).Model(&model.AgentTask{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":      status,
			"error_msg":   errMsg,
			"retry_count": retryCount,
		}).Error
}

// GetTaskByID 按 ID 查询单个任务（带租户过滤）。
// 查询同时满足 id 和 tenant_id，防止查到别的租户的任务。
// 记录不存在时返回 nil，err 为 gorm.ErrRecordNotFound。
func GetTaskByID(ctx context.Context, tenantID, id uint64) (*model.AgentTask, error) {
	var t model.AgentTask
	err := DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTasks 分页查询某租户的任务列表（带租户过滤）。
// 返回该页数据切片、符合条件的总条数以及可能的错误。
// 可按状态筛选（status=0 或空串表示不过滤）。
func ListTasks(ctx context.Context, tenantID uint64, page, pageSize int) ([]model.AgentTask, int64, error) {
	var list []model.AgentTask
	var total int64

	// 分页默认值兜底（避免负数或 0 导致查询异常）
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	// 租户过滤是底线
	query := DB.WithContext(ctx).Model(&model.AgentTask{}).Where("tenant_id = ?", tenantID)

	// 统计符合条件的任务总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 查当前页数据，按 ID 倒序（最新创建的任务在前）
	offset := (page - 1) * pageSize
	if err := query.
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}
