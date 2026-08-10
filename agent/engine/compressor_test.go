package engine

import (
	"strings"
	"testing"

	"agent-platform/agent/memory"
)

// buildHistory 构造 history 条轮次（每条 = 1 个 user + 1 个 assistant，成对交替）。
// 每轮 content 重复 rep 次以可控地撑大 token 数。
func buildHistory(rounds, rep int) []memory.ChatMessage {
	var h []memory.ChatMessage
	for i := 0; i < rounds; i++ {
		u := strings.Repeat("用户提问我想知道关于项目的一些背景信息", rep)
		a := strings.Repeat("助手回答好的我来说明一下这个项目的背景情况", rep)
		h = append(h,
			memory.ChatMessage{Role: memory.RoleUser, Content: u},
			memory.ChatMessage{Role: memory.RoleAssistant, Content: a},
		)
	}
	return h
}

// mustCompress 断言对历史进行压缩必然触发（用于构造"超阈值"样本）。
func mustCompress(t *testing.T, c *Compressor, h []memory.ChatMessage) []memory.ChatMessage {
	t.Helper()
	out, triggered, err := c.Compress(h)
	if err != nil {
		t.Fatalf("压缩出错: %v", err)
	}
	if !triggered {
		t.Fatalf("历史总 %d 条（>阈值）应触发压缩", len(h))
	}
	return out
}

func TestCompressorNeedsCompressShort(t *testing.T) {
	// 少量轮次 ≤ 阈值 → 不触发
	c := NewCompressor(nil)
	h := buildHistory(2, 1)
	if c.NeedsCompress(h) {
		t.Fatalf("短历史不应触发压缩")
	}
	out, triggered, err := c.Compress(h)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if triggered {
		t.Fatalf("短历史不应触发压缩")
	}
	if len(out) != len(h) {
		t.Fatalf("未压缩时应原样返回，len=%d", len(out))
	}
}

func TestCompressorLongTriggersAndKeepsRecent(t *testing.T) {
	// 构造一个远超阈值的多轮历史（10 轮，每轮够大）
	h := buildHistory(10, 60) // 每轮 240 token，10 轮 ≈ 4800 > 2000
	c := NewCompressor(nil)   // summarize=nil → 用占位摘要
	out := mustCompress(t, c, h)

	// 压缩后结构 = 1 条摘要 + 保留最近 keepRecentRounds 轮（每轮2条）
	want := 1 + 2*keepRecentRounds
	if len(out) != want {
		t.Fatalf("压缩后应 %d 条（1摘要+%d轮），得到 %d", want, keepRecentRounds, len(out))
	}

	// 第一条是 system 摘要
	if out[0].Role != memory.RoleSystem {
		t.Fatalf("第一条应为 system 摘要，得到 %v", out[0].Role)
	}
	if !strings.Contains(out[0].Content, "历史背景摘要") {
		t.Fatalf("摘要消息内容不符: %q", out[0].Content)
	}

	// 保留区应当等于原历史的最后 keepRecentRounds*2 条
	lastPart := h[len(h)-2*keepRecentRounds:]
	for i := 0; i < len(lastPart); i++ {
		if out[1+i].Content != lastPart[i].Content {
			t.Fatalf("保留区第%d条与原历史不符", i)
		}
	}
}

func TestCompressorUsesSummarizer(t *testing.T) {
	// 用 mock summarize 验证：摘要确实来自 summarize 的输出，且替换了旧历史
	var calledOld []memory.ChatMessage
	c := NewCompressor(func(old []memory.ChatMessage) (string, error) {
		calledOld = old
		return "自定义摘要：讨论项目背景与进展", nil
	})
	h := buildHistory(8, 60) // 超阈值
	out := mustCompress(t, c, h)

	// summarize 被调用，且接收到的旧历史 = 保留区之前的所有消息
	split := splitKeepRecent(h, keepRecentRounds)
	for i := 0; i < split; i++ {
		if calledOld[i].Content != h[i].Content {
			t.Fatalf("传给 summarizer 的旧历史不一致（下标记 %d）", i)
		}
	}

	// 摘要内容来自 mock
	if !strings.Contains(out[0].Content, "自定义摘要") {
		t.Fatalf("摘要应来自 summarize 输出, 实际 %q", out[0].Content)
	}
	// 且旧历史被替换（只剩摘要 + 保留区）
	if len(out) != 1+2*keepRecentRounds {
		t.Fatalf("长度异常: %d", len(out))
	}
}

func TestCompressorVeryLovHistKeepsAll(t *testing.T) {
	// 历史很少（未超短阈值）时：不压缩、全部保留、不生成摘要
	c := NewCompressor(func([]memory.ChatMessage) (string, error) {
		t.Fatal("短历史不应调用 summarizer")
		return "", nil
	})
	h := buildHistory(1, 2)
	out, triggered, err := c.Compress(h)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if triggered {
		t.Fatal("短历史不应触发")
	}
	if len(out) != len(h) {
		t.Fatalf("应完整保留，len=%d", len(out))
	}
}

func TestSplitKeepRecent(t *testing.T) {
	// 3 轮历史（3 assistant）→ 保留末尾 keepRecentRounds 轮
	h := buildHistory(3, 1) // 3 轮 = 6 条
	split := splitKeepRecent(h, keepRecentRounds)
	if split != 0 {
		t.Fatalf("3轮历史不足以产生压缩区，split应=0，得到 %d", split)
	}

	// 10 轮历史 → 压缩区保留前 10-3=7 轮，split 指向第8轮(user)开头
	h10 := buildHistory(10, 1)
	split10 := splitKeepRecent(h10, keepRecentRounds)
	// 10 轮共 20 条，保留最后 3 轮 = 6 条 → split = 20 - 6 = 14
	if split10 != 14 {
		t.Fatalf("10轮历史split应=14，得到 %d", split10)
	}
}

func TestCountHistoryTokens(t *testing.T) {
	// 每条消息都计入 content token + 角色开销
	h := []memory.ChatMessage{{Role: memory.RoleUser, Content: "你好"}}
	got := countHistoryTokens(h)
	// "你好"=2中文=4token + 4角色开销 = 8
	if got != 8 {
		t.Fatalf("countHistoryTokens 期望8，得到 %d", got)
	}
}
