package service

import (
	"context"

	"agent-platform/agent/interfaces"
	"agent-platform/llmclient"
	"agent-platform/storage"
)

// ============ Token 用量统计（业务层） ============
//
// 职责：
//  1. 实现 llmclient.UsageReporter 接口 —— 作为"用量回调钩子"注入 LLM 客户端。
//     每次 Chat/Embedding 调用完成后被回调，从 ctx 提取租户/用户标识，累加 Redis 计数。
//  2. 提供用量查询业务逻辑（当天 / 历史），供 handler 层调用。
//
// 复用既有钩子：llmclient 已在调用完成后触发 UsageReporter.Report(ctx, ev)，
// 并在调用前用 context.WithValue 注入了租户/用户（见 engine.go 里 WithTenantUser）。
// 因此这里不需要改业务调用链，只在 Report 里取指标累加即可 —— 面向接口、零侵入。

// UsageReporter 实现 llmclient.UsageReporter 接口，负责将每次 LLM 用量累加到 Redis。
type UsageReporter struct{}

// NewUsageReporter 构造一个用量上报实现，供 SetUsageReporter 注入 LLM 客户端。
func NewUsageReporter() *UsageReporter {
	return &UsageReporter{}
}

// Report 实现 llmclient.UsageReporter：
//   - 从 ev.Ctx 提取 tenant_id / user_id（由引擎在调用前经 interfaces.WithTenantUser 注入）；
//   - 成功调用才有 token 消耗，失败不累计（避免用量虚增）；
//   - 统计带 recover 兜底：即使 Redis 异常/panic 也不影响主流程（LLM 调用结果照常返回）。
func (r *UsageReporter) Report(ctx context.Context, ev llmclient.UsageEvent) {
	defer func() {
		// 用量统计失败绝不能影响对话主流程：任何 panic 都吞掉
		if err := recover(); err != nil {
			// fmt.Printf("[usage] 用量统计异常（已忽略）: %v\n", err)
		}
	}()

	// 只统计成功调用（失败无 token 产出，统计了也没意义且会虚增调用次数）
	if !ev.Success {
		return
	}

	tenantID := interfaces.TenantIDFromCtx(ctx)
	userID := interfaces.UserIDFromCtx(ctx)
	if tenantID == 0 {
		// 拿不到租户（理论上有 JWT 就有），忽略本次统计
		return
	}

	storage.AddUsage(ctx, tenantID, userID, storage.UsageStats{
		Tokens: int64(ev.Tokens.PromptTokens + ev.Tokens.CompletionTokens),
		Calls:  1,
	})
}

// ============ 用量查询（业务层） ============

// DayUsage 单日用量（token / calls）。
type DayUsage struct {
	Date   string `json:"date"`
	Tokens int64  `json:"tokens"`
	Calls  int64  `json:"calls"`
}

// GetTenantTodayUsage 查询某租户当天用量。返回 token 总数与调用次数。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage 使 Redis 用量统计日志带同一链路 ID。
func GetTenantTodayUsage(ctx context.Context, tenantID uint64) (tokens, calls int64) {
	t, c := storage.GetDayUsage(ctx, storage.DimTenant, tenantID, storage.UsageDate())
	return t, c
}

// GetTenantUsageHistory 查询某租户最近 days 天的用量趋势（含当天）。
// ctx 携带请求级 trace_id/tenant_id，透传给 storage。
// 仅返回确有记录的日子；按时间正序调用方展示。
func GetTenantUsageHistory(ctx context.Context, tenantID uint64, days int) ([]DayUsage, error) {
	m := storage.GetRangeUsage(ctx, storage.DimTenant, tenantID, days)
	if m == nil {
		return []DayUsage{}, nil
	}
	list := make([]DayUsage, 0, len(m))
	// 按日期排好序（map 无序，这里按字典序即时间序装配）
	// GetRangeUsage 内部已按 今天往前 days-1 天 的顺序扫描，这里为稳妥直接收集
	for d, v := range m {
		list = append(list, DayUsage{Date: d, Tokens: v[0], Calls: v[1]})
	}
	return list, nil
}
