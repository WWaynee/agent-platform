package service

import (
	"errors"
	"fmt"

	"agent-platform/storage"
	"agent-platform/storage/model"

	"gorm.io/gorm"
)

// ============ Service 层：异步任务业务逻辑 ============

// ConsumeDocumentParseTask 消费一条文档解析任务的完整处理编排（worker 消费者 handler 调用）。
//
// 流程：把任务的 document 走一遍现有解析主流程，并同步 agent_tasks 状态。
//  1. 校验任务存在（属于该租户），置为 processing
//  2. 调 ProcessDocument 做解析（读文件→切片→Embedding→写Qdrant→更新文档状态）
//  3. 成功 → 任务置 success、清空错误信息；失败 → 任务置 failed 并记录错误信息
//     （文档解析失败不重复入队，避免死循环；由重试机制/后台在必要时补偿）
//
// ⚠️ 返回 nil 表示处理成功；返回 error 表示处理失败（调用方（mq.Consume）据此决定
// ACK/Nack：这里我们选择"业务失败也 Nack 重新入队"，交由 mq.Consume 处理。
// 但为幂等与防死循环，实际由 ProcessDocument 的幂等点 ID 保证重复处理安全。
func ConsumeDocumentParseTask(taskID, tenantID, documentID uint64) error {
	// 1. 确认任务存在（多租户隔离：查任务也带 tenant_id）
	if _, err := storage.GetTaskByID(tenantID, taskID); err != nil {
		return fmt.Errorf("任务不存在或无权访问(task=%d): %w", taskID, err)
	}

	// 2. 更新任务为 processing（标记开始处理，供前端轮询）
	if err := storage.UpdateTaskStatus(taskID, "processing", ""); err != nil {
		return fmt.Errorf("更新任务为 processing 失败(task=%d): %w", taskID, err)
	}

	// 3. 执行文档解析主流程（内部已做文档的 pending→processing→success/failed 流转）
	if err := ProcessDocument(tenantID, documentID); err != nil {
		// 解析失败：任务置 failed，记录错误信息
		_ = storage.UpdateTaskStatus(taskID, "failed", err.Error())
		return err
	}

	// 4. 解析成功：任务置 success，清空错误信息
	if err := storage.UpdateTaskStatus(taskID, "success", ""); err != nil {
		return fmt.Errorf("更新任务为 success 失败(task=%d): %w", taskID, err)
	}
	return nil
}

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
