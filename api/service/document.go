package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"agent-platform/mq"
	"agent-platform/storage"
	"agent-platform/storage/model"

	"gorm.io/gorm"
)

// ============ Service 层：文档业务逻辑 ============

// UploadDocument 上传文档（异步化）
// 流程：上传文件到 MinIO → 写文档表(pending) → 写任务表(pending) → 发 MQ 消息 → 立即返回
//
// 入参说明：
//   - ctx：当前请求上下文（携带 trace_id），透传给 MQ 生产者，使投递日志带上链路 ID；
//   - tenantID：从 JWT 上下文拿（多租户安全：绝不信前端传的 tenant_id）
//   - userID：当前操作者
//   - filename：前端原始文件名（存入 name 字段，用于展示）
//   - size：文件字节大小
//   - file：文件内容流
//
// ⚠️ 异步化：上传只落 3 处元数据并投递消息，解析与向量化交给后台消费者处理
// （消费者从 mq 队列取消息 → 按 DocumentID 查文档 → 从 MinIO 拿文件 → 切片/向量/写库）。
// 因此本方法立即返回，用户无需等解析完成，可随时通过任务/文档状态查询进度。
func UploadDocument(ctx context.Context, tenantID, userID uint64, filename string, size int64, file io.Reader) (*model.Document, uint64, error) {
	// 1. 生成唯一 objectKey：{tenant_id}/{timestamp}_{filename}
	//    带 tenant_id 前缀：MinIO 内按租户分目录，方便管理与隔离
	//    带 timestamp：防止同名文件相互覆盖
	objectKey := fmt.Sprintf("%d/%d_%s", tenantID, time.Now().Unix(), filename)

	// 2. 上传文件到 MinIO（文件流直接透传，不落本地磁盘）
	if err := storage.UploadFile(objectKey, file, size); err != nil {
		return nil, 0, fmt.Errorf("上传文件到 MinIO 失败: %w", err)
	}

	// 3. 写文档表（status=pending，待解析）
	doc := &model.Document{
		TenantID:       tenantID,
		UserID:         userID,    // 上传者（当前登录用户）
		Name:           filename,  // 前端原始文件名，用于展示
		MinioObjectKey: objectKey, // MinIO 内的唯一存储路径
		Status:         "pending", // 待解析（后台消费者完成后置为 success）
		Size:           size,      // 文件字节大小
	}
	if err := storage.CreateDocument(ctx, doc); err != nil {
		return nil, 0, fmt.Errorf("写入文档记录失败: %w", err)
	}

	// 4. 写任务表（task_type=document_parse, biz_id=文档ID, status=pending）
	task := &model.AgentTask{
		TenantID: tenantID,
		TaskType: "document_parse",
		BizID:    doc.ID,    // 关联文档 ID
		Status:   "pending", // 待消费
	}
	if err := storage.CreateTask(ctx, task); err != nil {
		return nil, 0, fmt.Errorf("创建异步任务失败: %w", err)
	}

	// 5. 投递 MQ 消息（消息体只带 任务ID/租户ID/文档ID + trace_id/msg_id，消费者按 ID 去查库/取文件）
	//    发送失败：文档已上传但进不了处理队列，把文档状态置为 failed 防止悬空
	if err := mq.PublishDocumentParseTask(ctx, task.ID, tenantID, doc.ID); err != nil {
		_ = storage.UpdateDocumentStatus(ctx, doc.ID, "failed")
		return nil, 0, fmt.Errorf("投递异步任务消息失败: %w", err)
	}

	// 6. 立即返回文档（status=pending，后台异步处理中）
	return doc, task.ID, nil
}

// ListDocuments 分页查询文档列表
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使 DB 慢查询/错误日志带同一链路 ID。
// ⚠️ 强制按 tenant_id 过滤：只能查到当前租户的文档，绝不可跨租户
// 返回文档切片、总条数
func ListDocuments(ctx context.Context, tenantID uint64, page, pageSize int) ([]model.Document, int64, error) {
	return storage.ListDocuments(ctx, tenantID, page, pageSize)
}

// GetDocumentDetail 查询单个文档详情
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// ⚠️ 强制按 tenant_id 过滤：查不到（无论是不存在还是不属于当前租户）统一返回"文档不存在"
// 安全考虑：不区分"不存在"和"无权访问"，避免被探测是否存在别的租户的文档
func GetDocumentDetail(ctx context.Context, tenantID, id uint64) (*model.Document, error) {
	doc, err := storage.GetDocumentByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 不区分"文档不存在"与"跨租户访问"，统一报"文档不存在"
			return nil, fmt.Errorf("文档不存在")
		}
		return nil, fmt.Errorf("查询文档失败: %w", err)
	}
	return doc, nil
}

// DeleteDocument 删除文档
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// 流程：确认文档属于当前租户 → 删 MinIO 文件 → 软删数据库记录
// 只删 DB 记录而保留 MinIO 文件会造成孤儿文件、浪费存储，故两者一起删。
func DeleteDocument(ctx context.Context, tenantID, id uint64) error {
	// 1. 先查文档，确认存在且属于当前租户（带 tenant_id 过滤）
	doc, err := storage.GetDocumentByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("文档不存在")
		}
		return fmt.Errorf("查询文档失败: %w", err)
	}

	// 2. 删除 MinIO 里的实际文件
	if err := storage.DeleteFile(doc.MinioObjectKey); err != nil {
		return fmt.Errorf("删除 MinIO 文件失败: %w", err)
	}

	// 3. 软删数据库记录
	if err := storage.DeleteDocument(ctx, tenantID, id); err != nil {
		return fmt.Errorf("删除文档记录失败: %w", err)
	}

	return nil
}
