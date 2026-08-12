package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
	"agent-platform/api/service"
	"agent-platform/api/validator"
)

// ============ 请求参数结构体 ============

// CreateTenantReq 创建租户请求
type CreateTenantReq struct {
	Name          string `json:"name" binding:"required,min=1,max=64"` // 租户名称（必填，长度 1~64）
	AdminUsername string `json:"admin_username"`                       // 该租户首个管理员用户名（可选，默认 admin）
	AdminPassword string `json:"admin_password"`                       // 该租户首个管理员密码（可选，默认 admin123）
}

// ListTenantsReq 租户分页列表请求（query 参数）
type ListTenantsReq struct {
	Page     int `form:"page" binding:"omitempty,min=1"`              // 页码，从 1 起
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"` // 每页条数，1~100
}

// UpdateTenantStatusReq 更新租户状态请求
// 用 oneof 限制只能取 0 或 1（不能用 required：status=0(禁用)是合法值，
// required 会对数值零值误判为"未传"；oneof 允许 0）。
type UpdateTenantStatusReq struct {
	Status int8 `json:"status" binding:"oneof=0 1"` // 状态：1 启用 / 0 禁用
}

// ============ Handler 函数 ============

// CreateTenant 创建租户
func CreateTenant(c *gin.Context) {
	var req CreateTenantReq
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	tenant, err := service.CreateTenant(c.Request.Context(), req.Name, req.AdminUsername, req.AdminPassword)
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

	tenant, err := service.GetTenantDetail(c.Request.Context(), id)
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

	list, total, err := service.ListTenants(c.Request.Context(), req.Page, req.PageSize)
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
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	if err := service.UpdateTenantStatus(c.Request.Context(), id, req.Status); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, nil)
}
