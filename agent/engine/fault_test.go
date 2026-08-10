package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
)

// ============ 异常场景容错测试 ============
//
// 自测标准：各种异常场景服务都不 panic、有友好错误提示。
//   1. LLM 调用失败      → 降级返回友好兜底回答，不 panic（错误进 meta）
//   2. 工具调用失败      → 把错误信息喂给 LLM，让其决定下一步
//   3. 解析输出失败      → 重试一次；仍不行走兜底，不 panic
//   4. Redis 挂          → 服务不崩（见 redis 集成测试 TestFaultRedisDown_NoPanic）
//
// 复用了同包 acceptance_test.go 的 okTool / errTool / sequenceLLM，
// 以及 memory_test.go 的 captureLLM。

// errLLMFail 模拟 LLM 服务故障时的错误。
var errLLMFail = errors.New("LLM 服务调用失败")

// errLLM 每次 Chat 都返回错误，模拟 LLM 服务故障。
type errLLM struct{}

func (errLLM) Chat(ctx context.Context, req ChatRequest) (string, error) {
	return "", errLLMFail
}

// mustNotPanic 复用：断言 fn 内部不 panic（已在 acceptance_test.go 定义）。

// TestFault_LLMErrorDegradeNoPanic
// LLM 调用失败：降级返回友好兜底回答，不 panic，原始错误进 meta。
func TestFault_LLMErrorDegradeNoPanic(t *testing.T) {
	e := NewReActEngine(errLLM{}, toolmanager.NewToolManager(), memory.NewInMemoryMemory(), "你是一个助手")
	e.MaxIterations = 3

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "llm-down"}, "你好")
	})
	if err != nil {
		t.Fatalf("LLM 失败应降级为兜底回答而非返回错误, got err=%v", err)
	}
	if resp == nil || resp.Answer == "" {
		t.Fatal("LLM 失败应返回非空友好兜底回答")
	}
	if !strings.Contains(resp.Answer, "不可用") && !strings.Contains(resp.Answer, "稍后再试") {
		t.Fatalf("兜底回答应给用户友好提示, got %q", resp.Answer)
	}
	if resp.GetMeta("error") == nil {
		t.Fatal("降级响应应把原始错误塞进 meta 供审计")
	}
}

// TestFault_ToolErrorMessageVisibileToLLM
// 工具调用失败：错误信息应被喂回给 LLM（下一轮的 prompt 里能看到工具错误），让 LLM 决定怎么办。
func TestFault_ToolErrorMessageVisibileToLLM(t *testing.T) {
	llm := &captureLLM{
		replies: []string{
			`{"action":"err_tool","action_input":"x"}`, // 第1轮：调用会出错的工具
			`{"action":"final_answer","action_input":"工具出错了但我不 panic"}`,
		},
	}
	tm := toolmanager.NewToolManager()
	_ = tm.RegisterTool(errTool{})
	e := NewReActEngine(llm, tm, memory.NewInMemoryMemory(), "你是一个助手")

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "tool-msg"}, "试试")
	})
	if err != nil {
		t.Fatalf("工具失败不应返回错误, got %v", err)
	}
	if len(resp.ToolCalls) == 0 || resp.ToolCalls[0].ToolName != "err_tool" {
		t.Fatalf("应记录一次 err_tool 调用, got %+v", resp.ToolCalls)
	}
	// 第2轮（final_answer 前）LLM 收到的 prompt 应包含工具失败信息
	secondReq := llm.requests[len(llm.requests)-1]
	joined := ""
	for _, m := range secondReq.Messages {
		joined += m.Content + " "
	}
	if !strings.Contains(joined, "err_tool") || !strings.Contains(joined, "失败") {
		t.Fatalf("工具失败信息应喂给 LLM, got: %s", joined)
	}
}

// TestFault_ParseFailureRetryTwiceThenFallback
// 解析输出失败：重试一次仍失败后走兜底，全程不 panic、有兜底回答。
func TestFault_ParseFailureRetryTwiceThenFallback(t *testing.T) {
	// LLM 每次输出都是乱码（非 JSON）→ 重试一次仍失败 → 触发兜底
	llm := &sequenceLLM{replies: []string{"这不是JSON", "还是不是JSON", "依旧不是JSON"}}
	e := NewReActEngine(llm, toolmanager.NewToolManager(), memory.NewInMemoryMemory(), "你是一个助手")
	e.MaxIterations = 2

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "parse-fail"}, "问")
	})
	if err != nil {
		t.Fatalf("解析失败最终应兜底而非返回错误, got %v", err)
	}
	if resp == nil || resp.Answer == "" {
		t.Fatal("解析失败后应有兜底回答")
	}
}
