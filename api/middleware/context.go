package middleware

import "github.com/gin-gonic/gin"

// ============ Context 取值工具 ============
//
// 配套 JWTAuth() 使用：中间件把 user_id / tenant_id / role 注入 context，
// handler 通过这里的安全取值函数读取，避免手写 c.Get().(uint64) 的类型断言。
//
// 安全约定：拿不到值或类型不匹配时返回零值（不 panic），调用方据实际需要处理。
// 注意：uint64 的零值是 0，业务中正常的 user_id / tenant_id 从 1 起，可用 0 判断"未取到"。

// GetUserID 从 context 取 user_id
// 未取到或类型不符返回 0
func GetUserID(c *gin.Context) uint64 {
	if v, ok := c.Get("user_id"); ok {
		if id, ok := v.(uint64); ok {
			return id
		}
	}
	return 0
}

// GetTenantID 从 context 取 tenant_id
// 未取到或类型不符返回 0
func GetTenantID(c *gin.Context) uint64 {
	if v, ok := c.Get("tenant_id"); ok {
		if id, ok := v.(uint64); ok {
			return id
		}
	}
	return 0
}

// GetRole 从 context 取 role
// 未取到或类型不符返回空字符串
func GetRole(c *gin.Context) string {
	if v, ok := c.Get("role"); ok {
		if role, ok := v.(string); ok {
			return role
		}
	}
	return ""
}
