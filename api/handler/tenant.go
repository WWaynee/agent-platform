package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
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

// RegisterTenantReq 注册租户请求（公开接口）
// 不接收 tenant_id / role：租户 ID 由建租户自动生成，首个账号固定 role=admin（防提权）。
type RegisterTenantReq struct {
	Name          string `json:"name" binding:"required,min=1,max=64"`            // 租户名称（必填，长度 1~64）
	AdminUsername string `json:"admin_username" binding:"required,min=2,max=64"`  // 首个管理员用户名（必填，长度 2~64）
	AdminPassword string `json:"admin_password" binding:"required,min=6,max=128"` // 首个管理员密码（必填，长度 6~128）
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

// RegisterTenant 注册租户（公开接口，无需登录）
// 成功时原子创建「租户 + 首个管理员账号（role=admin）+ 默认工具配置」，
// 返回租户与管理员账号信息，调用方可立即用该管理员登录。
func RegisterTenant(c *gin.Context) {
	var req RegisterTenantReq
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	tenant, admin, err := service.RegisterTenant(c.Request.Context(), req.Name, req.AdminUsername, req.AdminPassword)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"tenant": tenant,
		"admin":  admin,
	})
}

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
// ⚠️ 多租户隔离（本次补强）：只能查「当前登录用户所属的租户」，路径 id 必须等于 JWT 的 tenant_id，
//    否则返回"租户不存在"（不区分"不存在"与"无权"，防横向探测 / 防枚举其它租户）。
func GetTenantDetail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的租户 ID")
		return
	}

	// 归属校验：只能查当前租户
	tenantID := middleware.GetTenantID(c)
	if tenantID == 0 || id != tenantID {
		response.BadRequest(c, "租户不存在")
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
