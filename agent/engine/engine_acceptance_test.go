package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"agent-platform/agent/interfaces"
	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
)

// ============ 验收测试：最大迭代限制 / 工具异常 / 解析重试，均须不 panic ============
//
// 对应周六最终验收清单：
//   - 最大迭代次数限制生效，不会无限循环
//   - 所有异常场景服务不 panic

// sequenceLLM 可预设多条输出序列的假 LLM，用于模拟多轮 ReAct。
type sequenceLLM struct {
	replies []string
	call    int
}

func (s *sequenceLLM) Chat(ctx context.Context, req ChatRequest) (string, error) {
	if s.call >= len(s.replies) {
		return "", fmt.Errorf("预设输出用尽(不应发生，可能陷入无限循环)")
	}
	r := s.replies[s.call]
	s.call++
	return r, nil
}

// okTool 正常工具：原样返回。
type okTool struct{}

func (okTool) Name() string        { return "ok_tool" }
func (okTool) Description() string { return "正常工具" }
func (okTool) Parameters() string  { return `{"type":"object"}` }
func (okTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	return "ok(" + params + ")", nil
}

// errTool 永远返回错误的工具，验证：工具执行异常时引擎不 panic。
type errTool struct{}

func (errTool) Name() string        { return "err_tool" }
func (errTool) Description() string { return "总会出错" }
func (errTool) Parameters() string  { return `{"type":"object"}` }
func (errTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	return "", fmt.Errorf("模拟工具内部故障")
}

// mustNotPanic 断言 fn 内部不 panic，返回其返回值。
func mustNotPanic(t *testing.T, fn func() (*AgentResponse, error)) (*AgentResponse, error) {
	t.Helper()
	var (
		resp *AgentResponse
		err  error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("发生 panic: %v", r)
			}
		}()
		resp, err = fn()
	}()
	return resp, err
}

// newTestEngineWith 构造带指定 LLM 与工具的引擎。
func newTestEngineWith(llm LLMClient, tools ...toolmanager.Tool) *ReActEngine {
	tm := toolmanager.NewToolManager()
	for _, t := range tools {
		if err := tm.RegisterTool(t); err != nil {
			panic(err)
		}
	}
	e := NewReActEngine(llm, tm, memory.NewInMemoryMemory(), "你是一个助手")
	return e
}

// TestAcceptance_MaxIterationsBound 最大迭代限制生效，不无限循环。
func TestAcceptance_MaxIterationsBound(t *testing.T) {
	// LLM 永远输出工具调用，从不 final_answer → 必须被 MaxIterations 拦住
	llm := &sequenceLLM{
		replies: []string{
			`{"action":"ok_tool","action_input":"1"}`,
			`{"action":"ok_tool","action_input":"2"}`,
			`{"action":"ok_tool","action_input":"3"}`,
			`{"action":"ok_tool","action_input":"4"}`,
			`{"action":"ok_tool","action_input":"5"}`,
			`{"action":"ok_tool","action_input":"6"}`,
			`{"action":"ok_tool","action_input":"7"}`,
		},
	}
	e := newTestEngineWith(llm, okTool{})
	e.MaxIterations = 5

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "max-iter"}, "请一直调工具")
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	// 工具调用次数不得超过 MaxIterations
	if len(resp.ToolCalls) > e.MaxIterations {
		t.Fatalf("工具调用 %d 次超过 MaxIterations=%d，可能无限循环", len(resp.ToolCalls), e.MaxIterations)
	}
	// 达上限应有非空兜底回答（不 panic）
	if resp.Answer == "" {
		t.Fatal("达最大迭代后应有兜底回答")
	}
	if len(resp.ToolCalls) != 5 {
		t.Fatalf("应恰好执行 5 次工具调用（直到上限），got %d", len(resp.ToolCalls))
	}
	if !strings.Contains(resp.Answer, "最大") && !strings.Contains(resp.Answer, "轮") {
		t.Logf("兜底回答: %s", resp.Answer)
	}
}

// TestAcceptance_ToolErrorNoPanic 工具执行异常时引擎继续观察、不 panic。
func TestAcceptance_ToolErrorNoPanic(t *testing.T) {
	// 第1轮让 LLM 调用会报错的工具，第2轮 final_answer
	llm := &sequenceLLM{
		replies: []string{
			`{"action":"err_tool","action_input":"x"}`,
			`{"action":"final_answer","action_input":"工具出错，我已处理"}`,
		},
	}
	e := newTestEngineWith(llm, errTool{})

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "tool-err"}, "触发工具错误")
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if resp.Answer != "工具出错，我已处理" {
		t.Fatalf("工具异常后应正常走完循环得到最终答案，got %q", resp.Answer)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ToolName != "err_tool" {
		t.Fatalf("应记录一次 err_tool 调用, got %+v", resp.ToolCalls)
	}
}

// TestAcceptance_ParseFailureRetryThenSucceed 解析失败重试后成功，不 panic。
func TestAcceptance_ParseFailureRetryThenSucceed(t *testing.T) {
	// 第1轮 LLM 输出乱码（解析失败），重试后第2轮正常 final_answer
	llm := &sequenceLLM{
		replies: []string{
			"抱歉我这边有点混乱，回复了一些没意义的文字。",
			`{"action":"final_answer","action_input":"重试后我正常回答了"}`,
		},
	}
	e := newTestEngineWith(llm, okTool{})

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "parse-retry"}, "随便问")
	})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if resp.Answer != "重试后我正常回答了" {
		t.Fatalf("解析失败重试后应得到最终答案, got %q", resp.Answer)
	}
}

// TestAcceptance_EmptyQueryNoPanic 空/异常输入不 panic，返回明确错误或兜底。
func TestAcceptance_EmptyQueryNoPanic(t *testing.T) {
	llm := &sequenceLLM{replies: []string{`{"action":"final_answer","action_input":"ok"}`}}
	e := newTestEngineWith(llm, okTool{})
	// 空 query：引擎不 panic（顶层 handler 会拦空 query，引擎本身也应健壮）
	_, _ = mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "empty"}, "")
	})
}
