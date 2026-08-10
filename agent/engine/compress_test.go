package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
)

// stubLLM 可控的 mock LLM：可预设返回的文本或错误；
// 记录收到的请求，便于断言摘要 prompt 是否正确拼接历史。
type stubLLM struct {
	reply   string
	err     error
	gotMsgs []Message
}

func (s *stubLLM) Chat(ctx context.Context, req ChatRequest) (string, error) {
	s.gotMsgs = append(s.gotMsgs, req.Messages...)
	if s.err != nil {
		return "", s.err
	}
	return s.reply, nil
}

// newTestEngine 构造一个挂 stubLLM + 内存记忆 的引擎，方便测试 compressHistory。
func newTestEngine(llm LLMClient) *ReActEngine {
	return NewReActEngine(llm, toolmanager.NewToolManager(), memory.NewInMemoryMemory(), "你是一个助手")
}

// TestCompressHistory_正常压缩
// 多轮超长历史 → 生成摘要 → 新历史 = 系统摘要 + 最近 keepRecentRounds 轮，且已写回 memory。
func TestCompressHistory_NormalCompress(t *testing.T) {
	llm := &stubLLM{reply: "用户咨询项目背景与进展，关注 RAG 落地。"}
	e := newTestEngine(llm)

	// 超阈值历史（10 轮，每轮 60 组词 ≈ 4800 token）
	history := buildHistory(10, 60)

	out, err := e.CompressHistory(context.Background(), AgentContext{TenantID: 1, SessionID: "s1"}, history)
	if err != nil {
		t.Fatalf("compressHistory 不应出错: %v", err)
	}

	// 函数应能正常调用、返回结构正确
	want := 1 + 2*keepRecentRounds
	if len(out) != want {
		t.Fatalf("压缩后应 %d 条（1摘要+%d轮），得到 %d", want, keepRecentRounds, len(out))
	}
	if out[0].Role != memory.RoleSystem {
		t.Fatalf("第一条应为 system 摘要, got %v", out[0].Role)
	}
	if !strings.Contains(out[0].Content, "对话历史摘要") || !strings.Contains(out[0].Content, "RAG") {
		t.Fatalf("摘要内容应来自 LLM, got %q", out[0].Content)
	}

	// 最近 N 轮保留原文
	lastPart := history[len(history)-2*keepRecentRounds:]
	for i := 0; i < len(lastPart); i++ {
		if out[1+i].Content != lastPart[i].Content {
			t.Fatalf("保留区第 %d 条原文丢失", i)
		}
	}

	// 已写回 Memory（重新读回应等于压缩结果）
	got := e.Memory.GetHistory(1, "s1")
	if len(got) != len(out) {
		t.Fatalf("Memory 写回长度 %d != %d", len(got), len(out))
	}
	if got[0].Role != memory.RoleSystem || got[0].Content != out[0].Content {
		t.Fatalf("Memory 首条应为摘要")
	}
}

// TestCompressHistory_降级
// LLM 摘要失败 → 降级为丢弃旧消息、只保留最近几轮原文，不返回错误，仍写回。
func TestCompressHistory_Degrade(t *testing.T) {
	llm := &stubLLM{err: errors.New("LLM 超时")}
	e := newTestEngine(llm)

	history := buildHistory(10, 60) // 超阈值
	out, err := e.CompressHistory(context.Background(), AgentContext{TenantID: 1, SessionID: "s1"}, history)
	if err != nil {
		t.Fatalf("压缩失败应降级而非返回错误，got err: %v", err)
	}

	// 降级后 = 只保留最近 N 轮原文，无摘要
	if len(out) != 2*keepRecentRounds {
		t.Fatalf("降级后应只剩最近 %d 轮（%d 条），得到 %d", keepRecentRounds, 2*keepRecentRounds, len(out))
	}
	for i, m := range out {
		if m.Role == memory.RoleSystem {
			t.Fatalf("降级后不应有 system 摘要，第 %d 条是 system", i)
		}
	}
	// 降级结果也被写回
	got := e.Memory.GetHistory(1, "s1")
	if len(got) != len(out) {
		t.Fatalf("降级写回长度 %d != %d", len(got), len(out))
	}
}

// TestCompressHistory_摘要Prompt包含历史
// 传给 LLM 的 user 消息应含旧消息的完整拼接。
func TestCompressHistory_PromptContainsHistory(t *testing.T) {
	llm := &stubLLM{reply: "摘要"}
	e := newTestEngine(llm)

	history := buildHistory(10, 60)
	_, _ = e.CompressHistory(context.Background(), AgentContext{TenantID: 1, SessionID: "s2"}, history)

	if len(llm.gotMsgs) == 0 {
		t.Fatalf("应调用 LLM 生成摘要")
	}
	// user 消息里应包含某条旧历史的原文片段
	userMsg := ""
	for _, m := range llm.gotMsgs {
		if m.Role == "user" {
			userMsg += m.Content
		}
	}
	if !strings.Contains(userMsg, "压缩成一段简洁的摘要") {
		t.Fatalf("user 消息应含摘要指令")
	}
	if !strings.Contains(userMsg, "用户提问") || !strings.Contains(userMsg, "助手回答") {
		t.Fatalf("user 消息应拼接旧历史内容")
	}
}

// TestCompressHistory_已含摘要不再重复压缩
// 历史第一条已是 system（刚压缩过）→ 不重复套娃压缩，原样返回。
func TestCompressHistory_AlreadyCompressed(t *testing.T) {
	llm := &stubLLM{} // reply 为空，不应被调用
	e := newTestEngine(llm)

	history := append(
		[]memory.ChatMessage{{Role: memory.RoleSystem, Content: "对话历史摘要：已有摘要"}},
		buildHistory(10, 60)...,
	)
	out, err := e.CompressHistory(context.Background(), AgentContext{TenantID: 1, SessionID: "s3"}, history)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(llm.gotMsgs) != 0 {
		t.Fatalf("已含摘要时不应再调用 LLM")
	}
	if len(out) != len(history) {
		t.Fatalf("已压缩历史应原样返回, len=%d", len(out))
	}
}

// TestCompressHistory_旧消息不足不压缩
// 历史很少、没有可压缩的旧消息 → 原样返回、不调用 LLM。
func TestCompressHistory_TooShort(t *testing.T) {
	llm := &stubLLM{}
	e := newTestEngine(llm)

	history := buildHistory(2, 1)
	out, err := e.CompressHistory(context.Background(), AgentContext{TenantID: 1, SessionID: "s4"}, history)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(llm.gotMsgs) != 0 {
		t.Fatalf("历史不足时不应调用 LLM")
	}
	if len(out) != len(history) {
		t.Fatalf("历史不足应原样返回, len=%d", len(out))
	}
}
