package handler

import (
	"errors"
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

	// 6. 调用业务层上传（异步：写文档表+任务表+投递 MQ 后立即返回）
	//    透传请求级 ctx：QAR 生产者据此把当前请求的 trace_id 写进 MQ 消息体，实现生产/消费同链路。
	doc, taskID, err := service.UploadDocument(c.Request.Context(), tenantID, userID, fileHeader.Filename, fileHeader.Size, file)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	// 7. 返回上传结果（status=pending，后台异步解析中）
	response.Success(c, gin.H{
		"id":        doc.ID,
		"name":      doc.Name,   // 文件名
		"status":    doc.Status, // pending
		"tenant_id": doc.TenantID,
		"task_id":   taskID, // 关联的异步任务 ID（可用它查询处理进度）
	})
}

// ============ 列表参数结构体 ============

// ListDocumentsReq 文档分页列表请求（query 参数）
type ListDocumentsReq struct {
	Page     int `form:"page" binding:"omitempty,min=1"`              // 页码，从 1 起（0 或空则用默认值）
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"` // 每页条数，1~100
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

	list, total, err := service.ListDocuments(c.Request.Context(), tenantID, req.Page, req.PageSize)
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
	doc, err := service.GetDocumentDetail(c.Request.Context(), tenantID, id)
	if err != nil {
		// 文档不存在/跨租户访问，统一返回 400（带出错提示）
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, doc)
}

// GetDocumentURL 获取文档的 OSS 签名访问 URL（预览 + 下载，需求单 0010）
// 返回 { url(预览inline), download_url(下载attachment), name }：预签名直链，浏览器直连 OSS。
// 多租户：tenant_id 从 JWT 拿，跨租户/不存在统一返回"文档不存在"。
func GetDocumentURL(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的文档 ID")
		return
	}
	tenantID := middleware.GetTenantID(c)
	previewURL, downloadURL, name, err := service.GetDocumentAccessURL(c.Request.Context(), tenantID, id)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{
		"url":          previewURL,
		"download_url": downloadURL,
		"name":         name,
	})
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
	userID := middleware.GetUserID(c)
	if err := service.DeleteDocument(c.Request.Context(), tenantID, userID, id); err != nil {
		// 文档不存在 / 跨租户 / 非本人文档 统一返回 400（含"无权删除他人文档"）
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

	if err := service.ProcessDocument(c.Request.Context(), tenantID, id); err != nil {
		// 文档不存在/跨租户访问：属于"资源不存在"，返回 400 而非 500
		if errors.Is(err, service.ErrDocumentNotFound) {
			response.BadRequest(c, err.Error())
			return
		}
		// 向量化过程真实失败（文件/Embedding/写库）：返回 500，文档状态已在 service 层置为 failed
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
