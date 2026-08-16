package engine

import (
	"context"
	"strings"
	"testing"

	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
)

// ============ 引擎正确使用 Memory：多轮上下文 + 历史持久化 ============
//
// 验收标准：
//   1. 多轮对话能记住上下文 —— 第二轮用的 Prompt 里应包含第一轮的问答
//   2. 历史存在 Redis/Memory 里 —— Run 之后能从中读回本轮 user/assistant
//
// 设计说明（中间过程存不存历史）：
//   - 本引擎选择【存最终问答】：persist 只把"本轮用户问题 query + 引擎最终回答 answer"
//     写回 Memory；中间轮次的 LLM 思考、工具观察结果**不写回 Memory**（只在当次请求内部
//     的 msgs 快照里即时消费）。理由：下一轮上下文只需知道"上一轮问了什么、答了什么"，
//     无需重放工具调用全过程；存得少则占 token 少、压缩更省。符合"存最终问答"建议。

// captureLLM 记录每次请求的完整 messages，便于断言"多轮 Prompt 是否带上历史"。
type captureLLM struct {
	replies  []string
	call     int
	requests []ChatRequest // 捕获每次调用收到的消息
}

func (c *captureLLM) Chat(ctx context.Context, req ChatRequest) (string, error) {
	c.requests = append(c.requests, req)
	if c.call >= len(c.replies) {
		return "", nil
	}
	r := c.replies[c.call]
	c.call++
	return r, nil
}

func (c *captureLLM) messagesJoined(attempt int) string {
	parts := make([]string, 0, len(c.requests[attempt].Messages))
	for _, m := range c.requests[attempt].Messages {
		parts = append(parts, m.Role+"|"+m.Content)
	}
	return strings.Join(parts, ";;")
}

// TestEngineMemory_MultiTurnRemembersContext
// 多轮对话能记住上下文：第二轮 Run 发往 LLM 的 prompt 里，应包含第一轮的问答内容。
func TestEngineMemory_MultiTurnRemembersContext(t *testing.T) {
	llm := &captureLLM{
		replies: []string{
			`{"action":"final_answer","action_input":"我叫小明，这是我的自我介绍。"}`,
			`{"action":"final_answer","action_input":"你好小明，记住我叫小红。"}`,
		},
	}
	e := NewReActEngine(llm, toolmanager.NewToolManager(), memory.NewInMemoryMemory(), "你是一个助手")

	ctx := AgentContext{TenantID: 1, SessionID: "sess-1"}

	// 第一轮问答
	if _, err := e.Run(ctx, "你叫什么名字？"); err != nil {
		t.Fatalf("第一轮 Run 失败: %v", err)
	}

	// 第二轮提问，让 LLM 回答"你知道我叫什么吗"——它需要读到上轮"我叫小明"
	if _, err := e.Run(ctx, "你知道我第一轮说的名字吗？"); err != nil {
		t.Fatalf("第二轮 Run 失败: %v", err)
	}

	// 第二轮第一次 LLM 调用收到的 prompt，应包含第一轮的"你叫什么名字"和答案"我叫小明"
	secondFirst := llm.messagesJoined(1) // 第二轮的首个 LLM 调用
	if !strings.Contains(secondFirst, "你叫什么名字") {
		t.Fatalf("第二轮 prompt 应带上上一轮的用户提问, got: %s", secondFirst)
	}
	if !strings.Contains(secondFirst, "我叫小明") {
		t.Fatalf("第二轮 prompt 应带上上一轮的助手回答, got: %s", secondFirst)
	}
}

// TestEngineMemory_HistoryPersistedToMemory
// 历史存进 Memory：Run 之后能从 Memory.GetHistory 读回本轮 user 提问与 assistant 回答。
func TestEngineMemory_HistoryPersistedToMemory(t *testing.T) {
	llm := &captureLLM{
		replies: []string{`{"action":"final_answer","action_input":"答案是42。"}`},
	}
	mem := memory.NewInMemoryMemory()
	e := NewReActEngine(llm, toolmanager.NewToolManager(), mem, "你是一个助手")

	if _, err := e.Run(AgentContext{TenantID: 1, SessionID: "sess-2"}, "6乘7等于几？"); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	hist := mem.GetHistory(1, "sess-2")
	if len(hist) != 2 {
		t.Fatalf("应存下 2 条（user 提问 + assistant 回答），got %d: %+v", len(hist), hist)
	}
	if hist[0].Role != memory.RoleUser || !strings.Contains(hist[0].Content, "6乘7等于几") {
		t.Fatalf("第一条应为 user 提问, got %+v", hist[0])
	}
	if hist[1].Role != memory.RoleAssistant || !strings.Contains(hist[1].Content, "42") {
		t.Fatalf("第二条应为 assistant 回答, got %+v", hist[1])
	}
}

// TestEngineMemory_MiddleToolProcessPersistedAsSummary
// 中间工具调用以"模板化摘要"写回热轨（需求单 0002：工具调用纳入完整历史与压缩）：
// 一轮里既调工具又 final_answer，Memory 里应为
//   [user(提问), user([工具] 模板化摘要), assistant(回答)]，共 3 条。
// 既让下一轮 LLM 记得上轮调过什么工具，又不把工具原文巨量塞进上下文。
func TestEngineMemory_MiddleToolProcessPersistedAsSummary(t *testing.T) {
	llm := &captureLLM{
		replies: []string{
			`{"action":"ok_tool","action_input":"1"}`,
			`{"action":"final_answer","action_input":"已根据工具结果回答。"}`,
		},
	}
	tm := toolmanager.NewToolManager()
	_ = tm.RegisterTool(okTool{})
	mem := memory.NewInMemoryMemory()
	e := NewReActEngine(llm, tm, mem, "你是一个助手")

	if _, err := e.Run(AgentContext{TenantID: 1, SessionID: "sess-3"}, "帮我查一下"); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	hist := mem.GetHistory(1, "sess-3")
	if len(hist) != 3 {
		t.Fatalf("应存 3 条: user(提问)+user(工具摘要)+assistant(回答), got %d: %+v", len(hist), hist)
	}
	if hist[0].Role != memory.RoleUser || !strings.Contains(hist[0].Content, "帮我查一下") {
		t.Fatalf("第 1 条应为用户提问, got %+v", hist[0])
	}
	// 第 2 条是工具模板化摘要：以 user 角色承载，内容以 [工具] 开头
	if hist[1].Role != memory.RoleUser || !strings.Contains(hist[1].Content, "[工具] ok_tool") {
		t.Fatalf("第 2 条应为工具模板化摘要(user 角色, [工具] 前缀), got %+v", hist[1])
	}
	if hist[2].Role != memory.RoleAssistant || !strings.Contains(hist[2].Content, "已根据工具结果回答") {
		t.Fatalf("第 3 条应为助手回答, got %+v", hist[2])
	}
}
