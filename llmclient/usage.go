package llmclient

import (
	"context"
	"sync"
	"time"
)

// ============ Token 用量统计封装 ============
//
// 目的：每次调用 Chat/Embedding 都会拿到 token 用量，这里统一封装，
// 一方面内置「累计用量统计」便于随时查看整体消耗，另一方面提供
// 「用量回调钩子（UsageReporter）」，供上层做租户用量统计 / 限流等，
// 而不需要改动客户端内部实现（面向接口、可扩展）。
//
// 设计要点：
//   - 回调通过接口 UsageReporter 注入，上层可实现任意采集逻辑（写库/日志/限流）。
//   - 回调携带「发起本次调用的上下文」：上层可在调用前用 context.WithValue
//     注入租户标识等，回调实现自行提取，无需改动调用接口（为租户统计/限流预留）。
//   - 内置 UsageStats 为并发安全的累计统计，可随时读取总量。

// Operation 表示一次调用的操作类型。
type Operation string

const (
	OperationChat   Operation = "chat"    // 对话调用
	OperationEmbed  Operation = "embed"   // 向量生成调用
)

// UsageEvent 描述一次 LLM 调用的用量与结果信息，随回调上报。
// 这是后续租户用量统计 / 限流的核心数据载体。
type UsageEvent struct {
	Ctx       context.Context // 发起本次调用的上下文（上层可用 WithValue 携带租户等标识）
	Operation Operation       // chat / embed
	Model     string          // 实际使用的模型名
	Tokens    TokenUsage      // 本次 token 用量（Prompt / Completion / Total）
	Success   bool            // 调用是否成功
	Duration  time.Duration   // 本次调用耗时
	Error     error           // 调用失败时的错误（Success==false 时非空）
}

// UsageReporter 用量上报接口：每次调用完成（成功或失败）都会触发 Report。
// 上层可注入自己的实现来做租户用量统计、持久化、限流决策等。
type UsageReporter interface {
	Report(ctx context.Context, ev UsageEvent)
}

// UsageStats 内置的并发安全累计用量统计，可随时读取整体消耗。
type UsageStats struct {
	mu             sync.Mutex
	ChatCalls      int      // Chat 调用总次数（含失败）
	EmbedCalls     int      // Embedding 调用总次数（含失败）
	PromptTokens   int64    // 累计 prompt token
	CompletionTokens int64  // 累计 completion token
	TotalTokens    int64    // 累计 total token
}

// add 累加一次调用（已带锁，由内部调用）。
func (s *UsageStats) add(op Operation, usage TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op == OperationChat {
		s.ChatCalls++
	} else {
		s.EmbedCalls++
	}
	s.PromptTokens += int64(usage.PromptTokens)
	s.CompletionTokens += int64(usage.CompletionTokens)
	s.TotalTokens += int64(usage.TotalTokens)
}

// Snapshot 返回当前累计统计的快照（供上层整体查看，不影响内部计数）。
func (s *UsageStats) Snapshot() (calls int, prompt int64, completion int64, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ChatCalls + s.EmbedCalls, s.PromptTokens, s.CompletionTokens, s.TotalTokens
}
