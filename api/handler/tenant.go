package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
	"agent-platform/api/service"
)

// ============ 请求参数结构体 ============

// CreateTenantReq 创建租户请求
type CreateTenantReq struct {
	Name string `json:"name" binding:"required"` // 租户名称
}

// ListTenantsReq 租户分页列表请求（query 参数）
type ListTenantsReq struct {
	Page     int `form:"page"`
	PageSize int `form:"page_size"`
}

// UpdateTenantStatusReq 更新租户状态请求
// 注意：不能用 binding:"required" 拦截状态，因为 status=0(禁用)是合法值，
// 而 required 对数值零值会误判为"未传"。因此靠 handler 内手动校验 0/1。
type UpdateTenantStatusReq struct {
	Status int8 `json:"status"` // 状态：1 启用 / 0 禁用
}

// ============ Handler 函数 ============

// CreateTenant 创建租户
func CreateTenant(c *gin.Context) {
	var req CreateTenantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	// 参数校验：租户名称不能为空
	if strings.TrimSpace(req.Name) == "" {
		response.BadRequest(c, "租户名称不能为空")
		return
	}

	tenant, err := service.CreateTenant(req.Name)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	response.Success(c, tenant)
}

// GetTenantDetail 租户详情
func GetTenantDetail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的租户 ID")
		return
	}

	tenant, err := service.GetTenantDetail(id)
	if err != nil {
		// 记录不存在时返回 400 或 404 语义，这里统一用 Fail(400) 便于前端处理
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, tenant)
}

// ListTenants 租户分页列表
func ListTenants(c *gin.Context) {
	var req ListTenantsReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	list, total, err := service.ListTenants(req.Page, req.PageSize)
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

// UpdateTenantStatus 更新租户状态
func UpdateTenantStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的租户 ID")
		return
	}

	var req UpdateTenantStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	// 参数校验：状态值只能是 0 或 1
	if req.Status != 0 && req.Status != 1 {
		response.BadRequest(c, "状态值只能为 0(禁用)或 1(启用)")
		return
	}

	if err := service.UpdateTenantStatus(id, req.Status); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, nil)
}
