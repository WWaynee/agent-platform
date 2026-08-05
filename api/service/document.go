package service

import (
	"fmt"
	"io"
	"time"

	"agent-platform/storage"
	"agent-platform/storage/model"
)

// ============ Service 层：文档业务逻辑 ============

// UploadDocument 上传文档
// 流程：上传文件到 MinIO → 构造文档元数据 → 写入 document 表 → 返回文档信息
//
// 入参说明：
//   - tenantID：从 JWT 上下文拿（多租户安全：绝不信前端传的 tenant_id）
//   - userID：当前操作者
//   - filename：前端原始文件名（存入 name 字段，用于展示）
//   - size：文件字节大小
//   - file：文件内容流
//
// status 先设为 pending：本阶段只做上传，尚未做解析与向量化；
// 待周六 RAG 阶段处理完成后，再改为 success（状态流转清晰，体现设计）。
func UploadDocument(tenantID, userID uint64, filename string, size int64, file io.Reader) (*model.Document, error) {
	// 1. 生成唯一 objectKey：{tenant_id}/{timestamp}_{filename}
	//    带 tenant_id 前缀：MinIO 内按租户分目录，方便管理与隔离
	//    带 timestamp：防止同名文件相互覆盖
	objectKey := fmt.Sprintf("%d/%d_%s", tenantID, time.Now().Unix(), filename)

	// 2. 上传文件到 MinIO（文件流直接透传，不落本地磁盘）
	if err := storage.UploadFile(objectKey, file, size); err != nil {
		return nil, fmt.Errorf("上传文件到 MinIO 失败: %w", err)
	}

	// 3. 构造文档元数据并写入 document 表
	doc := &model.Document{
		TenantID:       tenantID,
		Name:           filename,  // 前端原始文件名，用于展示
		MinioObjectKey: objectKey, // MinIO 内的唯一存储路径
		Status:         "pending", // 待解析（后续 RAG 阶段处理）
		Size:           size,      // 文件字节大小
	}
	// 注：userID 作为入参保留，但当前 document 表暂未记录"上传者"字段，
	// 如需后续体现"谁上传的"，可给 document 表增加 user_id / uploaded_by 列
	if err := storage.CreateDocument(doc); err != nil {
		return nil, fmt.Errorf("写入文档记录失败: %w", err)
	}

	return doc, nil
}
