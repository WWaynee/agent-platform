package storage

import (
	"context"

	"agent-platform/storage/model"
)

// ============ Storage 层：文档数据操作 ============
//
// 只跟数据库打交道，纯 CRUD，不写业务逻辑。
// 复用全局连接 DB（在 storage/mysql.go 中定义）。
// 每个方法都接收 ctx：用于把请求级 trace_id/tenant_id 透传给 GORM
// （经 DB.WithContext(ctx)），使慢查询/错误日志带同一链路 ID。
//
// ⚠️ 多租户关键约束：所有查询一律带 tenant_id 租户过滤，
// 绝不允许只按 ID 查询，否则会跨租户越权访问他人文档。

// ============ Storage 层：读全文 & 按名称搜索（get_document_content / search_documents / list_documents 用） ============

// DocumentText 某篇文档的全文内容（供 get_document_content 工具读取）。
//   - DocumentID:   文档 ID
//   - DocumentName: 文档名称（返回给 LLM 便于引用来源）
//   - Content:      全文原始文本（未截断；截断由工具层按字符上限负责）
//   - TotalChars:   原始总字符数（工具层据此判断是否超限并给出"已截断"提示）
type DocumentText struct {
	DocumentID   uint64
	DocumentName string
	Content      string
	TotalChars   int
}

// ReadDocumentContent 带租户过滤读取某篇文档的完整文本（供 get_document_content 工具）。
//  1. 先从 documents 表带 tenant_id 过滤取出文档（防跨租户越权读取）
//  2. 用 MinIO object key 下载对象并转为 UTF-8 文本返回
//
// ⚠️ 说明：能成功进入知识库的文档必然是 .txt/.md（ProcessDocument 的 ReadTextDocument 只放行这两种扩展名），
// 故此处直接 string(data) 转文本，无需重复扩展名校验。
// 多租户：tenant_id 强制过滤，绝不允许只按 documentID 读取他人文档。
func ReadDocumentContent(ctx context.Context, tenantID, documentID uint64) (*DocumentText, error) {
	doc, err := GetDocumentByID(ctx, tenantID, documentID)
	if err != nil {
		return nil, err
	}
	data, err := DownloadFile(doc.MinioObjectKey)
	if err != nil {
		return nil, err
	}
	content := string(data)
	return &DocumentText{
		DocumentID:   doc.ID,
		DocumentName: doc.Name,
		Content:      content,
		TotalChars:   len([]rune(content)),
	}, nil
}

// SearchDocuments 按文档名模糊搜索（name LIKE %keyword%），强制 tenant_id 过滤、排除软删。
// 返回当前租户中名称含 keyword 的文档（按更新时间倒序）。
//   - keyword 为空：返回该租户全部文档（等价不分页全量，供 list_documents 用）
//   - GORM 参数化 LIKE：天然防注入（特殊字符 %/_ 作为字面量处理）
//
// 供 search_documents 工具（文档多时按名称精确检索）与 list_documents 工具（全量列表）使用。
func SearchDocuments(ctx context.Context, tenantID uint64, keyword string) ([]model.Document, error) {
	query := DB.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if keyword != "" {
		// 参数化 LIKE，天然防注入
		query = query.Where("name LIKE ?", "%"+keyword+"%")
	}
	var list []model.Document
	if err := query.Order("updated_at DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// CreateDocument 插入一条文档记录
func CreateDocument(ctx context.Context, doc *model.Document) error {
	return DB.WithContext(ctx).Create(doc).Error
}

// GetDocumentByID 按 ID 查询单个文档（带租户过滤）
// 查询同时满足 id 和 tenant_id，防止查到别的租户的文档
func GetDocumentByID(ctx context.Context, tenantID, id uint64) (*model.Document, error) {
	var doc model.Document
	err := DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).First(&doc).Error
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// ListDocuments 分页查询某租户的文档列表（带租户过滤）
// 返回该页数据切片、符合条件的总条数以及可能的错误
func ListDocuments(ctx context.Context, tenantID uint64, page, pageSize int) ([]model.Document, int64, error) {
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
	if err := DB.WithContext(ctx).Model(&model.Document{}).
		Where("tenant_id = ?", tenantID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 查当前页数据
	offset := (page - 1) * pageSize
	if err := DB.WithContext(ctx).Model(&model.Document{}).
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
func DeleteDocument(ctx context.Context, tenantID, id uint64) error {
	return DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.Document{}).Error
}

// UpdateDocumentSummary 更新文档摘要（documents.summary 列）。
// 供 ProcessDocument 向量化成功后生成的 LLM 摘要落库（尽力而为，失败不影响主流程 success）。
// 带租户过滤更新，防止跨租户覆盖他人文档摘要。
func UpdateDocumentSummary(ctx context.Context, tenantID, id uint64, summary string) error {
	return DB.WithContext(ctx).Model(&model.Document{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Update("summary", summary).Error
}

// UpdateDocumentStatus 仅更新文档状态
// status 取值：pending / processing / success / failed
func UpdateDocumentStatus(ctx context.Context, id uint64, status string) error {
	return DB.WithContext(ctx).Model(&model.Document{}).
		Where("id = ?", id).
		Update("status", status).Error
}

// UpdateDocumentResult 更新文档状态，并可附带记录失败原因。
// 用于向量化流程结束后的状态落库：
//   - success：errorMsg 传空（成功无错误）
//   - failed ：errorMsg 记录失败原因
func UpdateDocumentResult(ctx context.Context, id uint64, status, errorMsg string) error {
	updates := map[string]interface{}{"status": status}
	// 只有失败才需要记录错误信息；成功时清空历史 error（避免残留上次的失败原因）
	if status == "failed" {
		updates["error_msg"] = errorMsg
	} else {
		updates["error_msg"] = nil
	}
	return DB.WithContext(ctx).Model(&model.Document{}).
		Where("id = ?", id).
		Updates(updates).Error
}
