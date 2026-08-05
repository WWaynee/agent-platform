package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
	"agent-platform/util"
)

// JWTAuth JWT 鉴权中间件
// 从 Authorization 请求头取出 Bearer token，解析校验后把 user_id / tenant_id / role 注入 context
// 解析失败或格式错误则返回 401 中断请求
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 取 Authorization 请求头
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Unauthorized(c, "未提供认证信息")
			c.Abort() // 中断请求，不再执行后续 handler
			return
		}

		// 2. 校验格式: Bearer xxx
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Unauthorized(c, "认证格式错误，应为 Bearer <token>")
			c.Abort()
			return
		}
		tokenString := strings.TrimSpace(parts[1])

		// 3. 解析 token
		claims, err := util.ParseToken(tokenString)
		if err != nil {
			response.Unauthorized(c, "token 无效或已过期")
			c.Abort()
			return
		}

		// 4. 把鉴权信息注入 context，供后续 handler / service 使用
		c.Set("user_id", claims.UserID)
		c.Set("tenant_id", claims.TenantID)
		c.Set("role", claims.Role)

		// 5. 放行，继续执行后续 handler
		c.Next()
	}
}
