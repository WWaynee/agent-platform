package handler

import (
	"path/filepath"
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
