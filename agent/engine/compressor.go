package engine

import (
	"fmt"

	"agent-platform/agent/memory"
	"agent-platform/util"
)

// ============ 上下文压缩策略 ============
//
// 目标：对话过长时自动压缩历史，使整个上下文不超出 LLM 的上下文窗口。
//
// 何时触发：每次加/读新消息前，检查该会话历史消息的总 token 数，超过阈值即触发压缩。
//
// 怎么压缩（比"直接删最早消息"更好）：
//   - 把"除最近 keepRecentRounds 轮之外"的旧对话，让 LLM 生成一段摘要；
//   - 用一条"系统摘要"消息代替那段旧历史；
//   - 保留最近几轮完整对话（不压缩），与摘要拼接，再接当前的新问题。
//
// 压缩后结构：
//   [system] 之前的对话大概讲了什么…（摘要）
//   [user]   最近一条问题（保留区最后一轮）
//   [assistant] 最近一条回答
//   [user]   当前问题（engine 组装 prompt 时追加）
//
// 为什么这么做：既保住了历史大意的信息，又把 token 量压在窗口内；保留最近几轮
// 保证连续对话的关键细节不被摘要磨平，比直接删消息效果好。

// ============ 压缩策略常量（阈值已定） ============

const (
	// compressThresholdTokens 触发压缩的历史消息 token 阈值。
	// 每次加新消息前，历史总 token 超过该值即触发压缩。
	compressThresholdTokens = 2000

	// keepRecentRounds 压缩时保留的「最近完整对话轮数」（不参与摘要压缩）。
	// 防止最新几轮的关键细节被摘要磨平，保证多轮连续对话质量。
	keepRecentRounds = 3

	// summaryPrompt 让 LLM 生成对话摘要时的系统提示。
	summaryPrompt = `你是对话摘要助手。请把给定的历史对话凝练成一段简洁的中文摘要，保留：用户的核心诉求、关键主题、已确定的信息与结论、用户提到的重要约束或偏好。
要求：
- 只输出摘要正文，不要任何解释或前后缀。
- 控制在 200 字以内。
- 不要编造对话中没有的信息。`

	// roleOverheadTokens 每条消息在拼接时的固定 token 开销（角色/分隔符），保守计入。
	roleOverheadTokens = 4
)

// ============ Compressor 组件 ============

// Summarizer 把一段历史对话生成一段摘要文本。
// 生产环境接真实 LLM；单测可注入 mock，便于验证压缩策略本身。
type Summarizer func(msgs []memory.ChatMessage) (string, error)

// Compressor 负责"检测超长 + 把超长旧历史压缩成摘要"。
// 策略参数（阈值 / 保留轮数）由上方常量确定。
type Compressor struct {
	summarize Summarizer
}

// NewCompressor 构造压缩器。
// summarize 实现摘要生成（生产用 LLM；测试可用 mock）。为 nil 时退化：超长时仍按
// 保留最近几轮来收紧，旧历史用一个固定提示占位（不强依赖摘要器可用）。
func NewCompressor(summarize Summarizer) *Compressor {
	return &Compressor{summarize: summarize}
}

// NeedsCompress 判断某段历史是否超过阈值、需要压缩。
func (c *Compressor) NeedsCompress(history []memory.ChatMessage) bool {
	return countHistoryTokens(history) > compressThresholdTokens
}

// countHistoryTokens 统计历史消息总 token 数。
// 叠加每条 content 的估算 token 数 + 固定角色开销；粗估即可，量级判断足够。
func countHistoryTokens(history []memory.ChatMessage) int {
	total := 0
	for _, m := range history {
		total += util.CountTokens(m.Content) + roleOverheadTokens
	}
	return total
}

// Compress 对历史实施压缩：
//   - 未超阈值 → 原样返回，triggered=false；
//   - 超阈值 → 把「除最近 keepRecentRounds 轮之外」的旧对话交给 summarize 生成摘要，
//     用一条 system 摘要消息替换旧历史，再接上保留的最近几轮返回，triggered=true。
//
// 返回值：(压缩后的消息序列, 是否真正触发了压缩, 错误)。错误可能来自摘要生成。
func (c *Compressor) Compress(history []memory.ChatMessage) ([]memory.ChatMessage, bool, error) {
	if len(history) == 0 || !c.NeedsCompress(history) {
		return history, false, nil
	}

	split := splitKeepRecent(history, keepRecentRounds)
	oldPart := history[:split] // 参与摘要的旧历史（最老的一段）
	recent := history[split:]  // 保留的最近几轮完整对话

	summary := "（早期对话因篇幅过长已折叠，重要背景如下）"
	if c.summarize != nil {
		s, err := c.summarize(oldPart)
		if err != nil {
			return history, false, fmt.Errorf("生成对话摘要失败: %w", err)
		}
		if s != "" {
			summary = s
		}
	}

	out := []memory.ChatMessage{
		{Role: memory.RoleSystem, Content: fmt.Sprintf("历史背景摘要：%s", summary)},
	}
	out = append(out, recent...)
	return out, true, nil
}

// splitKeepRecent 返回「保留区起始下标」。
// 保留最近的 keepRounds 轮完整对话（以 assistant 收尾计一轮），其之前的部分进压缩区。
//   - 历史总轮数 ≤ keepRounds：全部保留，返回 0（不产生压缩区）；
//   - 否则：保留后 keepRounds 轮 = 后 2*keepRounds 条消息，返回 len - 2*keepRounds。
func splitKeepRecent(history []memory.ChatMessage, keepRounds int) int {
	rounds := 0
	for _, m := range history {
		if m.Role == memory.RoleAssistant {
			rounds++
		}
	}
	if keepRounds <= 0 || rounds <= keepRounds {
		return 0
	}
	return len(history) - 2*keepRounds
}
