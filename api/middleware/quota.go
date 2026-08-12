package middleware

import (
	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
	"agent-platform/api/service"
)

// ============ 租户 Token 配额拦截中间件 ============
//
// 作用：超过配额的租户自动拦截 LLM 调用，体现 SaaS 平台的计费/配额管控能力。
//   - 只对"调 LLM 的接口"（对话 /api/chat）启用，普通接口（上传文档、查列表等）不做配额拦截。
//   - 放在限流中间件之后执行（限流 → 配额 → 业务）。
//
// 规则：
//   - 读租户表的 QuotaLlmToken（每月 token 配额）。0 = 不限制（兼容老租户，配额字段还没填）。
//   - 从 Redis 读该租户当月的 token 用量（按天累加，见 storage.GetMonthUsage）。
//   - 当月用量 ≥ 配额 → 返回 429/403，提示"本月 token 配额已用完"。
//   - 否则放行。

// QuotaInterceptor 返回一个配额拦截中间件。
// route/是否配额拦截由调用方决定（本中间件本身做通用判断）。
func QuotaInterceptor() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := GetTenantID(c)
		if tenantID == 0 {
			c.Next()
			return
		}

		over, quota := service.CheckTenantTokenQuota(tenantID)
		if over {
			// 超配额：返回固定 HTTP 状态码 + 明确提示，中断请求
			c.JSON(403, response.Body{
				Code:    403,
				Message: "本月 token 配额已用完，请升级套餐或联系管理员",
				Data:    gin.H{"quota": quota},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
