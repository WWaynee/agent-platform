package storage

import (
	"context"
	"fmt"
	"time"

	"agent-platform/config"
)

// ============ Token 用量统计（Redis 实时按天计数） ============
//
// 方案：不做 MySQL 持久化，只做 Redis 实时涨跌 + 保留最近 N 天。
//   - 每次 LLM 调用完成，由上层（api/service 的 UsageReporter 实现）调这里的
//     AddUsage 累加：租户维度 + 用户维度，按天 + 按 token/次数 两个指标。
//   - key 带日期，设置过期时间（保留最近 N 天），自动清理，无需定时任务。
//   - 用量查询接口直接从 Redis 读，支持查最近 N 天历史。
//
// key 设计（与 README / 步骤一致）：
//   - usage:tenant:{tenant_id}:{date}:token
//   - usage:tenant:{tenant_id}:{date}:calls
//   - usage:user:{user_id}:{date}:token
//   - usage:user:{user_id}:{date}:calls

// UsageStats 一次用量结算：token 数与调用次数，以及当前时间段（用于组 key）。
type UsageStats struct {
	Tokens int64 // 本次消耗 token 数（prompt + completion）
	Calls  int   // 本次新增调用次数（通常为 1）
}

// usageBaseKey 生成某维度 / 日期的基础 key 前缀（不含 :token/:calls 后缀）。
// dim：usageDimTenant / usageDimUser；date 形如 2026-01-01，来自 UsageDate。
func usageBaseKey(dim string, id uint64, date string) string {
	return fmt.Sprintf("usage:%s:%d:%s", dim, id, date)
}

// 维度关键字
const (
	usageDimTenant = "tenant"
	usageDimUser   = "user"
)

// UsageDate 返回今天的日期串（YYYY-MM-DD，按本地时区）。
func UsageDate() string {
	return time.Now().Format("2006-01-02")
}

// UsageTTL 返回用量 key 的过期时长（保留最近 N 天，默认 30）。
func UsageTTL() time.Duration {
	days := config.GlobalConfig.Usage.RedisTTL
	if days <= 0 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

// AddUsage 累加一次 LLM 调用产生的用量（成功调用才有 token；失败无消耗不计）。
// tenant/user 双维度各自累加 token 与 calls。Redis 不可用时静默跳过（不阻塞主流程）。
func AddUsage(ctx context.Context, tenantID, userID uint64, st UsageStats) {
	if RDB == nil || st.Tokens <= 0 && st.Calls <= 0 {
		return
	}
	date := UsageDate()
	ttl := UsageTTL()

	// 租户维度
	addUsageKey(ctx, usageBaseKey(usageDimTenant, tenantID, date), st, ttl)
	// 用户维度
	if userID > 0 {
		addUsageKey(ctx, usageBaseKey(usageDimUser, userID, date), st, ttl)
	}
}

// addUsageKey 对一个基础 key 累加 token 与 calls（分别 +INCR），并刷新过期时间。
func addUsageKey(ctx context.Context, base string, st UsageStats, ttl time.Duration) {
	tokenKey := base + ":token"
	callKey := base + ":calls"

	pipe := RDB.Pipeline()
	if st.Tokens > 0 {
		pipe.IncrBy(ctx, tokenKey, st.Tokens)
	}
	if st.Calls > 0 {
		pipe.Incr(ctx, callKey)
	}
	pipe.Expire(ctx, tokenKey, ttl)
	pipe.Expire(ctx, callKey, ttl)
	_, _ = pipe.Exec(ctx) // 失败静默，不影响主流程
}

// GetDayUsage 读取某维度某日期的用量（token / calls）。
// dim：usageDimTenant / usageDimUser；id：租户 ID / 用户 ID；date：YYYY-MM-DD。
// Redis 不可用或 key 不存在时返回未找到（全 0）不报错。
func GetDayUsage(ctx context.Context, dim string, id uint64, date string) (tokens, calls int64) {
	if RDB == nil {
		return 0, 0
	}
	base := usageBaseKey(dim, id, date)
	tokenKey := base + ":token"
	callKey := base + ":calls"

	v1, _ := RDB.Get(ctx, tokenKey).Int64()
	v2, _ := RDB.Get(ctx, callKey).Int64()
	return v1, v2
}

// GetMonthUsage 读取某维度当月累计 token 用量（供配额拦截用）。
// 数据按天计数，配额按"当月"比较：遍历当月每天 key 求和。
func GetMonthUsage(ctx context.Context, dim string, id uint64) (tokens int64) {
	if RDB == nil {
		return 0
	}
	now := time.Now()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	// 从 1 号到今天逐天累加
	for d := first; d.Month() == now.Month(); d = d.AddDate(0, 0, 1) {
		t, _ := GetDayUsage(ctx, dim, id, d.Format("2006-01-02"))
		tokens += t
		if d.Day() == now.Day() {
			break // 已到今天，停止（避免累加未来的日期）
		}
	}
	return tokens
}

// GetRangeUsage 读取某维度某日期区间（含当天往前 N-1 天）每天的用量，按天返回。
// 返回 map[date] = {tokens, calls}，仅包含确有记录的日子。
func GetRangeUsage(ctx context.Context, dim string, id uint64, days int) map[string][2]int64 {
	result := make(map[string][2]int64)
	if RDB == nil {
		return result
	}
	if days <= 0 {
		days = 30
	}
	for i := days - 1; i >= 0; i-- {
		d := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		t, c := GetDayUsage(ctx, dim, id, d)
		if t > 0 || c > 0 {
			result[d] = [2]int64{t, c}
		}
	}
	return result
}

// 便捷维度取值，供上层 GetDayUsage / GetRangeUsage 传 dim。
const (
	DimTenant = usageDimTenant
	DimUser   = usageDimUser
)
