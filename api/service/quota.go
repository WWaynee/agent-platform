package service

import (
	"context"

	"agent-platform/storage"
)

// ============ 租户 Token 配额（业务层） ============
//
// 规则：
//   - 租户表的 QuotaLlmToken 字段表示该租户每月允许消耗的 token 配额。
//   - 0 表示"不限制"（兼容老租户/未配置配额的租户）。
//   - 从 Redis 读该租户当月已用 token（用量统计按天计数，这里按当月求和）。
//   - 当月用量 >= 配额 → 判定超配额，应拦截 LLM 调用。
//
// QuotaLlmToken 为新增租户时写入默认值（见 CreateTenant 改进，新租户默认 100 万）。

// CheckTenantTokenQuota 校验某租户是否已超 token 配额。
// 返回 (是否已超配额, 该租户配额值)。配额为 0 表示不限制，永不超。
func CheckTenantTokenQuota(tenantID uint64) (over bool, quota int64) {
	// 1. 查租户配额
	tenant, err := storage.GetTenantByID(tenantID)
	if err != nil {
		// 租户查不到/DB 异常：保守不拦截（放行），避免因配额组件故障影响业务
		return false, 0
	}
	quota = tenant.QuotaLlmToken
	if quota <= 0 {
		// 0 = 不限制
		return false, quota
	}

	// 2. 读当月已用 token（租户维度）
	used := storage.GetMonthUsage(context.Background(), storage.DimTenant, tenantID)
	if used >= quota {
		return true, quota
	}
	return false, quota
}
