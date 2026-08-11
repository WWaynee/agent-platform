package service

import (
	"errors"
	"fmt"

	"agent-platform/storage"
	"agent-platform/storage/model"

	"gorm.io/gorm"
)

// ============ Service 层：异步任务业务逻辑 ============

// GetTaskDetail 查询单个异步任务的详情（状态 + 错误信息）。
//
// ⚠️ 强制按 tenant_id 过滤：只能查到当前租户的任务，绝不可跨租户。
// 任务不存在或属于别的租户，统一返回"任务不存在"——不区分这两种情况，
// 避免被探测到他人任务是否存在。
//
// 用途：前端上传文档后轮询此接口，看处理进度（pending→processing→success/failed）。
func GetTaskDetail(tenantID, id uint64) (*model.AgentTask, error) {
	task, err := storage.GetTaskByID(tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 不区分"任务不存在"与"跨租户访问"，统一报"任务不存在"
			return nil, fmt.Errorf("任务不存在")
		}
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	return task, nil
}
