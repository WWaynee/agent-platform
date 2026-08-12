package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
	"agent-platform/config"
	"agent-platform/storage"
)

// ============ 分布式限流中间件（租户级 + 用户级） ============
//
// 作用：防刷、保证多租户公平。从 JWT 上下文拿 tenant_id / user_id，
// 依次过两层滑动窗口限流：
//   - 租户级：每个租户每分钟最多 N 次（所有私有接口合计），防单租户打爆服务影响他人；
//   - 用户级：每个用户每分钟最多 M 次，防单个用户恶意刷接口。
//
// 两层都过才放行，任何一层超限都返回 429。
// 底层是 Redis 分布式滑动窗口（storage.AllowRequest），多实例部署也准确。
//
// tenant_id / user_id 一律从 JWT 上下文拿（已在 JWTAuth 注入），不带 token 的请求
// 不会进到这里（在 JWTAuth 就返回 401 了），因此不会消耗限流配额。

// tenantAllowed 判断租户级滑动窗口是否放行（内部只做判断，不碰 c.Next/c.Abort）。
func tenantAllowed(c *gin.Context) bool {
	tenantID := GetTenantID(c)
	if tenantID == 0 {
		return true // 拿不到租户（理论上鉴权后必有），不拦截
	}
	rl := config.GlobalConfig.RateLimit
	ok, _ := storage.AllowRequest(c.Request.Context(),
		storage.RateLimitTenantKeyPrefix, tenantID,
		rl.TenantPerMin,
		time.Duration(rl.WindowSec)*time.Second,
		time.Duration(rl.KeyTTL)*time.Second,
	)
	return ok
}

// userAllowed 判断用户级滑动窗口是否放行。
func userAllowed(c *gin.Context) bool {
	userID := GetUserID(c)
	if userID == 0 {
		return true
	}
	rl := config.GlobalConfig.RateLimit
	ok, _ := storage.AllowRequest(c.Request.Context(),
		storage.RateLimitUserKeyPrefix, userID,
		rl.UserPerMin,
		time.Duration(rl.WindowSec)*time.Second,
		time.Duration(rl.KeyTTL)*time.Second,
	)
	return ok
}

// chatAllowed 判断对话接口专属限流是否放行（按租户单独计数，阈值更低）。
func chatAllowed(c *gin.Context) bool {
	tenantID := GetTenantID(c)
	if tenantID == 0 {
		return true
	}
	rl := config.GlobalConfig.RateLimit
	ok, _ := storage.AllowRequest(c.Request.Context(),
		"ratelimit:chat:tenant:", tenantID,
		rl.ChatPerMin,
		time.Duration(rl.WindowSec)*time.Second,
		time.Duration(rl.KeyTTL)*time.Second,
	)
	return ok
}

// rateLimited 返回一个通用限流中间件，逐层执行给定维度决策函数；任一维度受限即 429。
func rateLimited(decisions ...func(*gin.Context) bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, decide := range decisions {
			if !decide(c) {
				abortTooMany(c)
				return
			}
		}
		c.Next()
	}
}

// RateLimiter 全局限流中间件：租户级 + 用户级。
// 供私有路由组整体挂载（所有私有接口生效）。
func RateLimiter() gin.HandlerFunc {
	return rateLimited(tenantAllowed, userAllowed)
}

// ChatRateLimiter 对话接口专属限流中间件（更严格，调 LLM 成本高），
// 叠加在通用限流之上（挂在 /api/chat 单独再限一层）。
func ChatRateLimiter() gin.HandlerFunc {
	return rateLimited(chatAllowed)
}

// abortTooMany 统一返回 429 Too Many Requests
func abortTooMany(c *gin.Context) {
	c.JSON(429, response.Body{
		Code:    429,
		Message: "请求过于频繁，请稍后再试",
		Data:    nil,
	})
	c.Abort()
}
