package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
	"agent-platform/api/validator"
)

// ============ 管理员创建员工（普通用户，member） ============
//
// POST /api/admin/user（挂 admin 组，JWT + AdminAuth 双重鉴权）
//   - 仅供租户管理员为「当前租户」创建普通成员。
//   - 安全边界：tenant_id 一律从 JWT 上下文拿（= 管理员自己所属租户），**不信前端 body 传入的租户**，
//     因此管理员只能给自己租户建员工，不能跨租户建。
//   - 角色固定 member：此接口不提供建管理员能力（提权口子已堵），管理员仍由建租户流程或另行管理。
//   - 用户名字段复用「全局唯一」约束（后端 registerInTx 已全库查重），重复则拒绝。

// AdminCreateUserReq 管理员创建员工请求体。
// 不含 tenant_id（从 JWT 取）也不含 role（固定 member），防越权与防提权。
type AdminCreateUserReq struct {
	Username string `json:"username" binding:"required,min=2,max=64"` // 用户名（全局唯一）
	Password string `json:"password" binding:"required,min=6,max=128"` // 密码
}

// AdminCreateUser 创建员工
func AdminCreateUser(c *gin.Context) {
	var req AdminCreateUserReq
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	// 租户 ID 从 JWT 取（管理员所属租户），不信 body
	tenantID := middleware.GetTenantID(c)
	if tenantID == 0 {
		response.Unauthorized(c, "未获取到租户信息")
		return
	}

	// 创建普通成员（固定 member）
	user, err := service.CreateUserByAdmin(c.Request.Context(), tenantID, req.Username, req.Password)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// 审计：记录管理员创建员工
	service.RecordAuditLog(c.Request.Context(), "创建员工",
		fmt.Sprintf("管理员创建普通员工 %s（tenant_id=%d, user_id=%d）", user.Username, user.TenantID, user.ID))

	response.Success(c, gin.H{
		"id":        user.ID,
		"tenant_id": user.TenantID,
		"username":  user.Username,
		"role":      user.Role,
	})
}
