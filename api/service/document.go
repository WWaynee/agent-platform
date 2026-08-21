package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
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
	// 审计：记录上传行为（尽力而为，不影响主流程）。
	RecordAuditLog(ctx, "上传文档", fmt.Sprintf("上传文档 %q（%d 字节，异步解析中）", filename, size))
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

// GetDocumentAccessURL 生成文档的 OSS 预签名访问 URL（预览 + 下载，需求单 0010）。
// 校验文档属于当前租户后取 objectKey，生成短时效（1 小时）签名 URL：
//   - url          预览用（inline，浏览器新页签打开）
//   - download_url 下载用（attachment，浏览器另存为）
func GetDocumentAccessURL(ctx context.Context, tenantID, id uint64) (previewURL, downloadURL, name string, err error) {
	doc, err := storage.GetDocumentByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", "", fmt.Errorf("文档不存在")
		}
		return "", "", "", fmt.Errorf("查询文档失败: %w", err)
	}
	pu, err := storage.PresignPreviewURL(doc.MinioObjectKey, time.Hour)
	if err != nil {
		return "", "", "", fmt.Errorf("生成文档预览链接失败: %w", err)
	}
	du, err := storage.PresignDownloadURL(doc.MinioObjectKey, doc.Name, time.Hour)
	if err != nil {
		return "", "", "", fmt.Errorf("生成文档下载链接失败: %w", err)
	}
	return pu, du, doc.Name, nil
}

// GetDocumentPreview 读取文档全文用于内联预览（需求单 0010 review 修复：预览走后端代理，保证内联展示）。
// 校验文档属于当前租户后从 OSS 读回全文，返回纯文本内容 + 文件名 + 内容类型。
func GetDocumentPreview(ctx context.Context, tenantID, id uint64) (content, name, contentType string, err error) {
	doc, err := storage.GetDocumentByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", "", fmt.Errorf("文档不存在")
		}
		return "", "", "", fmt.Errorf("查询文档失败: %w", err)
	}
	data, err := storage.DownloadFile(doc.MinioObjectKey)
	if err != nil {
		return "", "", "", fmt.Errorf("读取文档内容失败: %w", err)
	}
	ct := "text/plain; charset=utf-8"
	switch strings.ToLower(filepath.Ext(doc.Name)) {
	case ".md":
		ct = "text/markdown; charset=utf-8"
	}
	return string(data), doc.Name, ct, nil
}

// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// 流程：确认文档属于当前租户 → 校验拥有权（仅上传者可删）→ 删 MinIO 文件 → 软删数据库记录
// 只删 DB 记录而保留 MinIO 文件会造成孤儿文件、浪费存储，故两者一起删。
//
// ⚠️ 用户级隔离：文档也像会话一样按上传者隔离——只有上传该文档的用户（userID 匹配）
//
//	才能删除它。成员不能删他人（含管理员）上传的文档，防止越权破坏他人/公共知识库内容。
func DeleteDocument(ctx context.Context, tenantID, userID, id uint64) error {
	// 1. 先查文档，确认存在且属于当前租户（带 tenant_id 过滤）
	doc, err := storage.GetDocumentByID(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("文档不存在")
		}
		return fmt.Errorf("查询文档失败: %w", err)
	}

	// 2. 用户级隔离：只能删自己上传的文档（同租户内也不能删别人的）
	if doc.UserID != userID {
		return fmt.Errorf("无权删除他人文档")
	}

	// 3. 删除 MinIO 里的实际文件
	if err := storage.DeleteFile(doc.MinioObjectKey); err != nil {
		return fmt.Errorf("删除 MinIO 文件失败: %w", err)
	}

	// 3.5 删除该文档在 Qdrant 里的全部向量（数据一致性：文档删了，向量不能留孤儿）
	//     Qdrant 不可用时不能静默吞掉，否则留下脏向量；报错交给上层处理。
	if err := storage.DeleteVectorsByDocument(ctx, tenantID, id); err != nil {
		return fmt.Errorf("删除文档向量失败: %w", err)
	}

	// 4. 软删数据库记录
	if err := storage.DeleteDocument(ctx, tenantID, id); err != nil {
		return fmt.Errorf("删除文档记录失败: %w", err)
	}

	// 审计：记录删除行为（尽力而为，不影响主流程）。
	RecordAuditLog(ctx, "删除文档", fmt.Sprintf("删除文档 %q（ID=%d）", doc.Name, doc.ID))
	return nil
}
