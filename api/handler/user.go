package handler

import (
	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
	"agent-platform/api/validator"
	"agent-platform/util"
)

// ============ 请求参数结构体 ============

// RegisterReq 用户注册请求
type RegisterReq struct {
	TenantID uint64 `json:"tenant_id" binding:"required"`                // 租户 ID
	Username string `json:"username" binding:"required,min=2,max=64"`    // 用户名
	Password string `json:"password" binding:"required,min=6,max=128"`   // 密码
	Role     string `json:"role" binding:"omitempty,oneof=admin member"` // 角色（可选，默认 member；仅允许 admin/member，防注非法角色）
}

// LoginReq 用户登录请求
type LoginReq struct {
	TenantID uint64 `json:"tenant_id" binding:"required"`              // 租户 ID
	Username string `json:"username" binding:"required,min=2,max=64"`  // 用户名
	Password string `json:"password" binding:"required,min=6,max=128"` // 密码
}

// ============ Handler 函数 ============

// Register 用户注册
func Register(c *gin.Context) {
	var req RegisterReq
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	user, err := service.Register(c.Request.Context(), req.TenantID, req.Username, req.Password, req.Role)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, user)
}

// Login 用户登录
func Login(c *gin.Context) {
	var req LoginReq
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	user, err := service.Login(c.Request.Context(), req.TenantID, req.Username, req.Password)
	if err != nil {
		// 登录失败统一返回 401（不区分用户不存在/密码错误，防探测）
		response.FailStatus(c, 401, response.CodeUnauthorized, err.Error())
		return
	}

	// 登录成功，签发 JWT（payload 只含 user_id / tenant_id / role）
	token, err := util.GenerateToken(user.ID, user.TenantID, user.Role)
	if err != nil {
		response.ServerError(c, "生成 token 失败")
		return
	}

	response.Success(c, gin.H{
		"token": token,
		"user":  user,
	})
}

// GetUserInfo 当前登录用户信息（测试接口，放在私有路由组）
// 从 JWT 中间件注入的 Context 中读取 user_id / tenant_id / role 并返回
func GetUserInfo(c *gin.Context) {
	userID := middleware.GetUserID(c)
	tenantID := middleware.GetTenantID(c)
	role := middleware.GetRole(c)

	// 正常情况下中间件已保证这些值存在；若为 0 说明上下文没注入（异常情况）
	response.Success(c, gin.H{
		"user_id":   userID,
		"tenant_id": tenantID,
		"role":      role,
	})
}
