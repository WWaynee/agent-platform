package handler

import (
	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
)

// ============ 用量查询接口（租户管理员） ============
//
//   - GET /api/admin/usage/today        → 当前租户当天 token 用量与调用次数
//   - GET /api/admin/usage/history?days=N → 当前租户最近 N 天的用量趋势
//
// 多租户安全：tenant_id 一律从 JWT 上下文（唯一可信来源）取，只能查自己的租户。
// 本组路由已挂 AdminAuth（仅管理员可调）。

// GetUsageToday 查询当前租户当天用量
// 返回：{tokens, calls}
func GetUsageToday(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	tokens, calls := service.GetTenantTodayUsage(tenantID)
	response.Success(c, gin.H{
		"tokens": tokens,
		"calls":  calls,
	})
}

// GetUsageHistory 查询当前租户最近 N 天用量趋势
// 入参：days（可选，默认 7，上限 30）
// 返回：{list: [{date, tokens, calls}, ...]}（按日期升序）
func GetUsageHistory(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	days := 7 // 默认查询最近 7 天
	if v := c.Query("days"); v != "" {
		if n := parsePositiveInt(v); n > 0 {
			days = n
		}
	}
	// 上限 30 天（Redis 只保留最近 30 天）
	if days > 30 {
		days = 30
	}

	list, err := service.GetTenantUsageHistory(tenantID, days)
	if err != nil {
		response.ServerError(c, "查询用量历史失败: "+err.Error())
		return
	}

	response.Success(c, gin.H{"list": list})
}

// parsePositiveInt 把纯数字字符串解析为 int；非纯数字或溢出时返回 0。
func parsePositiveInt(s string) int {
	if s == "" {
		return 0
	}
	sum := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		sum = sum*10 + int(ch-'0')
		if sum > 1<<30 { // 简单防溢出
			return 0
		}
	}
	return sum
}
