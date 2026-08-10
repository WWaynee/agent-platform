package memory

import (
	"context"
	"fmt"

	"agent-platform/util"
)

// ============ 超长自动压缩记忆（装饰器） ============
//
// 目标：让「对话过长自动压缩」成为 Memory 的内部行为，业务层无感知。
//
// 为什么放 Memory 层：
//   - 引擎（上层）只管 GetHistory（拿历史）、AddMessage（加消息），不关心压缩逻辑；
//   - 压缩是 Memory 自身维护的内部逻辑——在每次追加消息后检查长度，超长就自动压缩，
//     符合单一职责，对上层完全透明。
//
// 实现方式：装饰器（Wrapper）。CompressingMemory 包裹一个底层 Memory（Redis/内存版），
// 在 AddMessage 里叠加"检查长度 + 超长自动压缩"，其余方法原样透传底层。
//
// 压缩需要生成摘要（依赖 LLM）——通过注入的 Summarizer 完成，Memory 不直接碰 LLM。
//
// 触发时机：每次 AddMessage 后检查；历史总 token 超阈值（约 2000）即触发。
//
// 注意点（保证可用性与不频繁压缩）：
//   - 同步执行：对话频率不高，同步做简单可靠；
//   - 摘要失败降级：LLM 摘要失败时不中断，降级为丢弃旧消息、保留最近几轮原文；
//   - 避免套娃：历史首条已是 system 摘要（刚压缩过）则本轮不再重复压缩。
type CompressingMemory struct {
	base   Memory     // 底层真实存储（Redis / 内存版）
	sum    Summarizer // 摘要生成（engine 注入，用 LLM；可为 nil）
	ctx    context.Context
	rdbCap bool // 仅用于区分是否 Redis 底层，默认 false（透传语义足够）

	// 由构造决定；本包提供默认压缩参数
}

// 压缩默认参数（阈值 / 保留轮数 / 单条角色开销 token），与 engine 压缩策略保持一致。
const (
	// CompressThresholdTokens 历史消息总 token 的压缩阈值，超过即触发自动压缩。
	CompressThresholdTokens = 2000
	// compKeepRecentRounds 压缩时保留的「最近完整对话轮数」，其前旧历史参与摘要。
	compKeepRecentRounds = 3
	// roleOverheadTokens 每条消息在 token 估算中的固定角色开销（保守计入）。
	roleOverheadTokens = 4
)

// NewCompressingMemory 构造超长自动压缩记忆。
// base 为底层存储；summarize 提供摘要生成（可传 nil，则超长时降级为直接丢弃旧历史）。
func NewCompressingMemory(base Memory, summarize Summarizer) *CompressingMemory {
	return &CompressingMemory{base: base, sum: summarize}
}

// GetHistory 透传底层历史。
func (c *CompressingMemory) GetHistory(tenantID uint64, sessionID string) []ChatMessage {
	return c.base.GetHistory(tenantID, sessionID)
}

// Clear 透传底层清空。
func (c *CompressingMemory) Clear(tenantID uint64, sessionID string) {
	c.base.Clear(tenantID, sessionID)
}

// Truncate 透传底层截断。
func (c *CompressingMemory) Truncate(tenantID uint64, sessionID string, maxTokens int) {
	c.base.Truncate(tenantID, sessionID, maxTokens)
}

// AddMessage 追加消息；追加后检查长度，超长则自动压缩——业务层无感知。
func (c *CompressingMemory) AddMessage(tenantID uint64, sessionID string, msg ChatMessage) {
	c.base.AddMessage(tenantID, sessionID, msg)
	if c.shouldCompress(tenantID, sessionID) {
		c.compress(tenantID, sessionID)
	}
}

// shouldCompress 判断该会话历史是否需要压缩：超阈值 且 非重复压缩（首条不是 system 摘要）。
func (c *CompressingMemory) shouldCompress(tenantID uint64, sessionID string) bool {
	hist := c.base.GetHistory(tenantID, sessionID)
	if len(hist) == 0 {
		return false
	}
	// 历史首条是 system 摘要 → 刚压缩过，不套娃
	if hist[0].Role == RoleSystem {
		return false
	}
	return countHistoryTokens(hist) > CompressThresholdTokens
}

// compress 执行压缩：拆新旧 → 生成摘要 → 组装 → 写回底层。
func (c *CompressingMemory) compress(tenantID uint64, sessionID string) {
	hist := c.base.GetHistory(tenantID, sessionID)

	split := compSplitKeepRecent(hist, compKeepRecentRounds)
	oldPart := hist[:split]
	recent := hist[split:]
	if len(oldPart) == 0 {
		return // 无可压缩的旧消息
	}

	summary := "（早期对话因篇幅过长已折叠，重要背景如下）"
	if c.sum != nil {
		if s, err := c.sum.Summarize(oldPart); err == nil && s != "" {
			summary = s
		} else if err != nil {
			// 摘要失败 → 降级：丢弃旧消息，只保留最近几轮原文
			c.writeBack(tenantID, sessionID, append([]ChatMessage(nil), recent...))
			return
		}
	}

	compressed := append(
		[]ChatMessage{{Role: RoleSystem, Content: fmt.Sprintf("对话历史摘要：%s", summary)}},
		recent...,
	)
	c.writeBack(tenantID, sessionID, compressed)
}

// writeBack 把整段历史写回底层（先清空再按序写）。
func (c *CompressingMemory) writeBack(tenantID uint64, sessionID string, history []ChatMessage) {
	c.base.Clear(tenantID, sessionID)
	for _, m := range history {
		c.base.AddMessage(tenantID, sessionID, m)
	}
}

// countHistoryTokens 统计历史总 token（content 估算 + 每条固定角色开销）。
func countHistoryTokens(history []ChatMessage) int {
	total := 0
	for _, m := range history {
		total += util.CountTokens(m.Content) + roleOverheadTokens
	}
	return total
}

// compSplitKeepRecent 返回保留区起始下标：保留最近 keepRounds 轮完整对话（以 assistant 收尾计一轮）。
func compSplitKeepRecent(history []ChatMessage, keepRounds int) int {
	rounds := 0
	for _, m := range history {
		if m.Role == RoleAssistant {
			rounds++
		}
	}
	if keepRounds <= 0 || rounds <= keepRounds {
		return 0
	}
	return len(history) - 2*keepRounds
}
