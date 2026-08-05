package handler

import (
	"strings"

	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
	"agent-platform/api/service"
	"agent-platform/util"
)

// ============ 请求参数结构体 ============

// RegisterReq 用户注册请求
type RegisterReq struct {
	TenantID uint64 `json:"tenant_id" binding:"required"` // 租户 ID
	Username string `json:"username" binding:"required"`  // 用户名
	Password string `json:"password" binding:"required"`  // 密码
	Role     string `json:"role" binding:"required"`      // 角色：admin / member
}

// LoginReq 用户登录请求
type LoginReq struct {
	TenantID uint64 `json:"tenant_id" binding:"required"` // 租户 ID
	Username string `json:"username" binding:"required"`  // 用户名
	Password string `json:"password" binding:"required"`  // 密码
}

// ============ Handler 函数 ============

// Register 用户注册
func Register(c *gin.Context) {
	var req RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}
	// 密码不能为空字符串
	if strings.TrimSpace(req.Password) == "" {
		response.BadRequest(c, "密码不能为空")
		return
	}

	user, err := service.Register(req.TenantID, req.Username, req.Password, req.Role)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, user)
}

// Login 用户登录
func Login(c *gin.Context) {
	var req LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	user, err := service.Login(req.TenantID, req.Username, req.Password)
	if err != nil {
		// 登录失败统一返回 401（不区分用户不存在/密码错误，防探测）
		response.FailStatus(c, 401, response.CodeUnauthorized, err.Error())
		return
	}

	// 登录成功，签发 JWT
	token, err := util.GenerateToken(user.ID, user.TenantID, user.Username, user.Role)
	if err != nil {
		response.ServerError(c, "生成 token 失败")
		return
	}

	response.Success(c, gin.H{
		"token": token,
		"user":  user,
	})
}
