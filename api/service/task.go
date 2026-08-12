package service

import (
	"context"
	"errors"
	"fmt"

	"agent-platform/mq"
	"agent-platform/storage"
	"agent-platform/storage/model"

	"gorm.io/gorm"
)

// ============ Service 层：异步任务业务逻辑 ============

// maxTaskRetry 文档解析任务的最大重试次数。
// 超过此次数仍失败，视为最终失败（任务置 failed、消息 ACK 丢弃，不再无限重试）。
const maxTaskRetry = 3

// ConsumeDocumentParseTask 消费一条文档解析任务的完整处理编排（worker 消费者 handler 调用）。
//
// ctx：消费者传入的消息级上下文（携带从消息体取回的 trace_id / tenant_id），
// 贯穿到 ProcessDocument 及其内部 Embedding/Qdrant 调用，使整条消费处理日志带同一 trace_id。
//
// 完整流程（对齐需求）：
//  1. 校验任务存在（属于该租户）
//  2. 更新任务状态 → processing；更新文档状态 → processing
//  3. 调 ProcessDocument 做文档解析（读文件→切片→Embedding→写Qdrant→成功置文档 success/failed）
//  4. 成功 → 更新任务 → success，清空错误
//  5. 失败 → 累加 retry_count：
//     - 未到上限：记录本次错误到任务表，返回包装 mq.ErrRequeue 的错误 → 消息重新入队重试
//     - 达到上限：任务置 failed、记录最终错误，返回普通 error → 消息 ACK 丢弃（不再无限循环）
//
// 文档状态：由 ProcessDocument 内部负责置 processing/success/failed；本方法只额外保证
// 任务侧的状态流转与重试计数。
//
// ⚠️ 返回值含义（供 mq.Consume 决定 ACK 策略）：
//   - nil → 成功（Ack）
//   - errors.Is(err, mq.ErrRequeue) → 需要重新入队重试（Nack requeue）
//   - 其他 error → 最终失败（Ack 丢弃，不再无限重试）
func ConsumeDocumentParseTask(ctx context.Context, taskID, tenantID, documentID uint64) error {
	// 1. 确认任务存在（多租户隔离：查任务也带 tenant_id）
	task, err := storage.GetTaskByID(ctx, tenantID, taskID)
	if err != nil {
		// 任务不存在/非本租户：无法重试，返回普通错误让消息 ACK 丢弃
		return fmt.Errorf("任务不存在或无权访问(task=%d): %w", taskID, err)
	}

	// 1.5 幂等检查：先查文档，若已是 success，说明之前某次消费已成功完成（可能重复投递/ACK 前重试），
	//    直接跳过解析并把任务置 success，避免重复切片/Embedding/写库（结果一致，但省资源、幂等）。
	doc, err := storage.GetDocumentByID(ctx, tenantID, documentID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 文档不存在：解析无从谈起，无法重试，Ack 丢弃
			return fmt.Errorf("文档不存在或无权访问(doc=%d): %w", documentID, err)
		}
		return fmt.Errorf("查询文档失败(doc=%d): %w", documentID, err)
	}
	if doc.Status == "success" {
		// 幂等短路：文档已处理成功，直接确认任务成功，不重复处理
		_ = storage.UpdateTaskStatus(ctx, taskID, "success", "")
		return nil
	}

	// 2. 更新任务 → processing；更新文档 → processing（供前端轮询）
	if err := storage.UpdateTaskStatus(ctx, taskID, "processing", ""); err != nil {
		return fmt.Errorf("更新任务为 processing 失败(task=%d): %w", taskID, err)
	}
	if err := storage.UpdateDocumentStatus(ctx, documentID, "processing"); err != nil {
		return fmt.Errorf("更新文档为 processing 失败(doc=%d): %w", documentID, err)
	}

	// 3. 执行文档解析主流程（内部已做文档状态流转 + 成功置 success / 失败置 failed）
	if err := ProcessDocument(ctx, tenantID, documentID); err != nil {
		return handleParseFailure(ctx, task, err)
	}

	// 4. 解析成功：任务置 success，清空错误信息
	if err := storage.UpdateTaskStatus(ctx, taskID, "success", ""); err != nil {
		return fmt.Errorf("更新任务为 success 失败(task=%d): %w", taskID, err)
	}
	return nil
}

// handleParseFailure 处理一次解析失败：累加重试次数并按上限决策是否重新入队。
//
//   - 重试次数未到上限：把当前错误记入任务表、累加 retry_count、状态保持 processing，
//     返回包装 mq.ErrRequeue 的错误 → mq.Consume 会让消息重新入队再试。
//   - 已达到上限：任务置 failed、记录最终错误，返回普通错误 → mq.Consume 会 ACK 丢弃，
//     避免无限循环；错误信息已落任务表供排查。
func handleParseFailure(ctx context.Context, task *model.AgentTask, parseErr error) error {
	nextRetry := task.RetryCount + 1

	if nextRetry < maxTaskRetry {
		// 未到上限：记录错误 + 累加重试次数，返回可重试信号
		_ = storage.UpdateTaskRetry(ctx, task.ID, nextRetry, "processing", parseErr.Error())
		return fmt.Errorf("%w: 第%d次处理失败: %v", mq.ErrRequeue, nextRetry, parseErr)
	}

	// 达到上限：最终失败，记录错误，Ack 丢弃
	_ = storage.UpdateTaskStatus(ctx, task.ID, "failed", parseErr.Error())
	return fmt.Errorf("达到最大重试次数(%d)仍失败: %w", maxTaskRetry, parseErr)
}

// GetTaskDetail 查询单个异步任务的详情（状态 + 错误信息）。
//
// ⚠️ 强制按 tenant_id 过滤：只能查到当前租户的任务，绝不可跨租户。
// 任务不存在或属于别的租户，统一返回"任务不存在"——不区分这两种情况，
// 避免被探测到他人任务是否存在。
//
// 用途：前端上传文档后轮询此接口，看处理进度（pending→processing→success/failed）。
func GetTaskDetail(ctx context.Context, tenantID, id uint64) (*model.AgentTask, error) {
	task, err := storage.GetTaskByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 不区分"任务不存在"与"跨租户访问"，统一报"任务不存在"
			return nil, fmt.Errorf("任务不存在")
		}
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	return task, nil
}
