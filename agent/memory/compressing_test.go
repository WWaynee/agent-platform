package memory

import (
	"errors"
	"strings"
	"testing"
)

// errSummarizeFail 模拟摘要生成失败的错误。
var errSummarizeFail = errors.New("LLM 摘要超时")

// stubSummarizer mock 摘要器：可预设返回/错误，记录被摘要的旧消息。
type stubSummarizer struct {
	reply    string
	err      error
	calledOn []ChatMessage
}

func (s *stubSummarizer) Summarize(msgs []ChatMessage) (string, error) {
	s.calledOn = msgs
	if s.err != nil {
		return "", s.err
	}
	return s.reply, nil
}

// tokenCount 统计历史的总 token（content 估算 + 角色开销），用于断言压缩前后的量级。
func tokenCount(hist []ChatMessage) int {
	t := 0
	for _, m := range hist {
		t += len([]rune(m.Content))/2 + 4
	}
	return t
}

// TestCompressingMemory_ShortNotCompress
// 短对话（远低于阈值）→ 不触发压缩，不出现 system 摘要。
func TestCompressingMemory_ShortNotCompress(t *testing.T) {
	sum := &stubSummarizer{}
	cm := NewCompressingMemory(NewInMemoryMemory(), sum)

	cm.AddMessage(1, "s", ChatMessage{Role: RoleUser, Content: "你好"})
	cm.AddMessage(1, "s", ChatMessage{Role: RoleAssistant, Content: "你好，有什么可以帮你？"})

	hist := cm.GetHistory(1, "s")
	if len(hist) != 2 {
		t.Fatalf("短对话应有 2 条，得到 %d", len(hist))
	}
	if hist[0].Role == RoleSystem {
		t.Fatalf("短对话不应被压缩（首条不应是 system 摘要）")
	}
	if len(sum.calledOn) != 0 {
		t.Fatalf("短对话不应调用摘要器")
	}
}

// TestCompressingMemory_LongAutoCompress
// 通过 AddMessage 累计超阈值后，应自动触发压缩（历史首条变 system 摘要）。
func TestCompressingMemory_LongAutoCompress(t *testing.T) {
	sum := &stubSummarizer{reply: "摘要：用户反复咨询 RAG 检索优化方案。"}
	cm := NewCompressingMemory(NewInMemoryMemory(), sum)

	// 持续追加较长消息，直到累计超阈值
	long := strings.Repeat("这是一段较长对话内容用于累计token数量超过阈值", 30)
	for i := 0; i < 30; i++ {
		cm.AddMessage(1, "s", ChatMessage{Role: RoleUser, Content: long})
		cm.AddMessage(1, "s", ChatMessage{Role: RoleAssistant, Content: long})
	}

	hist := cm.GetHistory(1, "s")
	if len(hist) == 0 || hist[0].Role != RoleSystem {
		t.Fatalf("长对话应被自动压缩，首条应为 system 摘要, 实际首条 role=%v", hist[0].Role)
	}
	// 摘要内容来自 mock
	if !strings.Contains(hist[0].Content, "RAG") {
		t.Fatalf("摘要应来自摘要器, 实际 %q", hist[0].Content)
	}
	// 摘要器收到的旧消息非空(确实发生了一次压缩)
	if len(sum.calledOn) == 0 {
		t.Fatalf("应把旧历史交给摘要器")
	}
	// 后续继续 AddMessage 会再累积，所以只验证"发生过自动压缩"即可，
	// 不猜测精确条数（套娃保护使首条摘要保留，历史继续增长为正常行为）。
	if hist[0].Role != RoleSystem {
		t.Fatalf("自动压缩后历史首条应为 system 摘要")
	}
}

// TestCompressingMemory_TokensReduced
// 验证"压缩后 token 数降下来了"：先把超长历史直接写入底层，触发一次 Auto 压缩，
// 对比压缩前后 token 量级。
func TestCompressingMemory_TokensReduced(t *testing.T) {
	sum := &stubSummarizer{reply: "摘要：一段关于系统性能优化与监控指标的对话背景。"}
	base := NewInMemoryMemory()
	cm := NewCompressingMemory(base, sum)

	// 直接往底层塞一批长历史（绕过压缩层），模拟已累积到超多 token；
	// 最后几轮故意用短消息，使"保留区"本身很小，从而压缩后 token 明显下降。
	long := strings.Repeat("这是一段内容非常丰富的历史对话讨论包括向量检索中文分块阈值等细节方案", 60)
	for i := 0; i < 10; i++ {
		base.AddMessage(1, "s", ChatMessage{Role: RoleUser, Content: long})
		base.AddMessage(1, "s", ChatMessage{Role: RoleAssistant, Content: long})
	}
	// 末尾追加 3 轮短消息 → 会成为"最近保留轮"，保证压缩后很小
	for i := 0; i < 3; i++ {
		base.AddMessage(1, "s", ChatMessage{Role: RoleUser, Content: "简短问题"})
		base.AddMessage(1, "s", ChatMessage{Role: RoleAssistant, Content: "简短回答"})
	}

	before := tokenCount(cm.GetHistory(1, "s"))
	if before <= CompressThresholdTokens {
		t.Fatalf("前置历史应远超阈值(before=%d)", before)
	}

	// 触发一次 AddMessage → 应自动压缩（压缩后首条 system 摘要）
	cm.AddMessage(1, "s", ChatMessage{Role: RoleUser, Content: "当前问题"})
	hist := cm.GetHistory(1, "s")
	if len(hist) == 0 || hist[0].Role != RoleSystem {
		t.Fatalf("应触发自动压缩，首条应为 system 摘要, 实际 %v", hist[0].Role)
	}

	after := tokenCount(hist)
	// 核心验收：压缩后 token 数降下来了
	if after >= before {
		t.Fatalf("压缩后 token 未下降: before=%d after=%d", before, after)
	}
	if after > CompressThresholdTokens {
		t.Fatalf("压缩后仍超阈值: before=%d after=%d", before, after)
	}
}

// TestCompressingMemory_SummarizeFailDegrade
// 摘要失败 → 降级为丢弃旧消息、只保留最近几轮，不中断，不出现 system 摘要。
func TestCompressingMemory_SummarizeFailDegrade(t *testing.T) {
	sum := &stubSummarizer{err: errSummarizeFail}
	cm := NewCompressingMemory(NewInMemoryMemory(), sum)

	long := strings.Repeat("超过阈值的历史内容用于触发压缩", 30)
	for i := 0; i < 30; i++ {
		cm.AddMessage(1, "s", ChatMessage{Role: RoleUser, Content: long})
		cm.AddMessage(1, "s", ChatMessage{Role: RoleAssistant, Content: long})
	}

	hist := cm.GetHistory(1, "s")
	if len(hist) != 2*compKeepRecentRounds {
		t.Fatalf("降级后应只剩最近 %d 轮(%d条), 实际 %d", compKeepRecentRounds, 2*compKeepRecentRounds, len(hist))
	}
	for _, m := range hist {
		if m.Role == RoleSystem {
			t.Fatalf("降级后不应有 system 摘要")
		}
	}
}

// TestCompressingMemory_TransparentToList
// 其余方法(GetHistory/Clear/Truncate)应透传底层——列表/清除能力不被压缩逻辑破坏。
func TestCompressingMemory_TransparentToList(t *testing.T) {
	sum := &stubSummarizer{}
	cm := NewCompressingMemory(NewInMemoryMemory(), sum)

	cm.AddMessage(1, "a", ChatMessage{Role: RoleUser, Content: "x"})
	cm.AddMessage(1, "b", ChatMessage{Role: RoleUser, Content: "y"})

	// 不同会话隔离仍成立（多租户+多会话存储不受影响）
	a := cm.GetHistory(1, "a")
	b := cm.GetHistory(1, "b")
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("多会话隔离被破坏")
	}

	// Clear 透传
	cm.Clear(1, "a")
	if len(cm.GetHistory(1, "a")) != 0 {
		t.Fatalf("Clear 未透传")
	}
}
