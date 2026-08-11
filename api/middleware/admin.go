package middleware

import (
	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
)

// AdminAuth 管理员权限中间件
// 在 JWTAuth() 之后挂载：从 context 取 login 的 role。
//   - role == "admin" → 放行
//   - 否则 → 返回 403 无权限，中断请求
//
// 为什么用中间件而不是在每个 handler 里判断？
//   统一拦截，避免每个管理接口 handler 都写一遍 role 判断；
//   哪些接口是管理员接口（挂了这个中间件）一目了然。
//
// 使用前提：必须跟在 JWTAuth() 后面（它保证 context 里已有 role）。
// 若 role 为空（正常不会出现，除非中间件顺序错了）也按无权限处理，保守拒绝。
func AdminAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		if role != "admin" {
			response.Forbidden(c, "无权限：仅管理员可操作")
			c.Abort()
			return
		}
		c.Next()
	}
}
