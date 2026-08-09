package handler

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
)

// ============ 上传参数与限制 ============

// 允许上传的文件格式（当前阶段仅支持文本类，PDF 等后续再支持）
var allowedExt = map[string]bool{
	".txt": true,
	".md":  true,
}

// 单个文件大小上限（10MB）
const maxUploadSize = 10 << 20

// ============ Handler 函数 ============

// UploadDocument 上传文档
// multipart form，字段名 file
// tenant_id / user_id 均从 JWT 中间件注入的 Context 拿（不信前端传的）
func UploadDocument(c *gin.Context) {
	// 1. 从上下文拿当前登录用户与租户
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)
	if tenantID == 0 {
		response.Unauthorized(c, "未获取到租户信息")
		return
	}

	// 2. 从请求里拿文件
	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "请上传文件，字段名为 file")
		return
	}

	// 3. 校验文件大小
	if fileHeader.Size > maxUploadSize {
		response.BadRequest(c, "文件过大，最大支持 10MB")
		return
	}

	// 4. 校验文件格式（仅允许 .txt / .md）
	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	if !allowedExt[ext] {
		response.BadRequest(c, "仅支持 .txt / .md 格式文件")
		return
	}

	// 5. 打开文件流
	file, err := fileHeader.Open()
	if err != nil {
		response.BadRequest(c, "读取文件失败")
		return
	}
	defer file.Close()

	// 6. 调用业务层上传
	doc, err := service.UploadDocument(tenantID, userID, fileHeader.Filename, fileHeader.Size, file)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	// 7. 返回上传结果
	response.Success(c, gin.H{
		"id":        doc.ID,
		"name":      doc.Name,   // 文件名
		"status":    doc.Status, // pending
		"tenant_id": doc.TenantID,
	})
}

// ============ 列表参数结构体 ============

// ListDocumentsReq 文档分页列表请求（query 参数）
type ListDocumentsReq struct {
	Page     int `form:"page"`
	PageSize int `form:"page_size"`
}

// ============ Handler 函数 ============

// ListDocuments 文档分页列表
// ⚠️ tenant_id 从 JWT 上下文拿，强制过滤，只能查当前租户的文档
func ListDocuments(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req ListDocumentsReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	list, total, err := service.ListDocuments(tenantID, req.Page, req.PageSize)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"list":      list,
		"total":     total,
		"page":      req.Page,
		"page_size": req.PageSize,
	})
}

// GetDocumentDetail 文档详情
// 路径参数：id
// tenant_id 从 JWT 拿，强制过滤；跨租户/不存在统一返回"文档不存在"
func GetDocumentDetail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的文档 ID")
		return
	}

	tenantID := middleware.GetTenantID(c)
	doc, err := service.GetDocumentDetail(tenantID, id)
	if err != nil {
		// 文档不存在/跨租户访问，统一返回 400（带出错提示）
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, doc)
}

// DeleteDocument 删除文档
// 路径参数：id
// tenant_id 从 JWT 拿，强制过滤；先删 MinIO 文件再软删 DB 记录
func DeleteDocument(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的文档 ID")
		return
	}

	tenantID := middleware.GetTenantID(c)
	if err := service.DeleteDocument(tenantID, id); err != nil {
		// 文档不存在/跨租户访问统一返回 400
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, nil)
}

// ProcessDocument 触发文档向量化（测试/调试接口）
// 路径参数：id（文档 ID）
//
// ⚠️ 当前为同步执行：接口返回时即向量化完成（或失败）。
// 暂不引入 MQ 异步队列（下周再加）——今天目标是把 RAG 链路打通，
// 同步调用便于调试排查。调用方拿到 success/failed 状态即知结果。
//
// 多租户安全：tenant_id 从 JWT 上下文拿，强制过滤；只能处理当前租户的文档。
func ProcessDocument(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的文档 ID")
		return
	}

	tenantID := middleware.GetTenantID(c)
	if tenantID == 0 {
		response.Unauthorized(c, "未获取到租户信息")
		return
	}

	if err := service.ProcessDocument(tenantID, id); err != nil {
		// 向量化失败：把错误信息返回给调用方（文档状态已在 service 层置为 failed）
		response.ServerError(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"id":        id,
		"status":    "success",
		"message":   "文档向量化完成",
		"tenant_id": tenantID,
	})
}
