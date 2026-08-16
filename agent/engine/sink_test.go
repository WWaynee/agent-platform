package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
)

// ============ 冷轨完整历史 sink 组装测试 ============
//
// 对应需求单 0002 5.1 第 3 条：`persist` 旁路写入——一轮含工具调用的对话后，
// FullHistorySink（冷轨 MySQL adapter 的替身）应按真实时序收到
//   [question, tool_call(工具名+参数), tool_result(执行结果), answer]，
// 且字段（tenant/user/session/role/kind/content）正确、工具结果不截断（全文交给冷轨）。

// fakeSink 内存版 FullHistorySink，按收到顺序记录每条 ChatMsg，供断言冷轨组装结果。
type fakeSink struct {
	mu   sync.Mutex
	msgs []ChatMsg
}

func (f *fakeSink) Append(_ context.Context, m ChatMsg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, m)
	return nil
}

// wait 轮询等待收集满 want 条消息（persistFullHistory 是后台 goroutine 写，需等待）。
func (f *fakeSink) wait(want int) []ChatMsg {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.msgs)
		msgs := append([]ChatMsg(nil), f.msgs...) // copy
		f.mu.Unlock()
		if n >= want {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	return f.msgs
}

// TestPersistFullHistory_AssemblesToolCallSequence
// 一轮含工具调用（ok_tool param=1）后 final_answer，冷轨 sink 应收到 4 条，
// 顺序 = question → tool_call → tool_result → answer，且各字段正确。
func TestPersistFullHistory_AssemblesToolCallSequence(t *testing.T) {
	llm := &captureLLM{ // engine/memory_test.go 里的假 LLM，按序返回
		replies: []string{
			`{"action":"ok_tool","action_input":"1"}`,
			`{"action":"final_answer","action_input":"销售额是1200万。"}`,
		},
	}
	tm := toolmanager.NewToolManager()
	if err := tm.RegisterTool(okTool{}); err != nil {
		t.Fatalf("注册工具失败: %v", err)
	}
	mem := memory.NewInMemoryMemory()
	e := NewReActEngine(llm, tm, mem, "你是一个助手")

	sink := &fakeSink{}
	e.SetFullHistorySink(sink)

	// 走一轮：先调 ok_tool(1)，参数 {"action_input":"1"} → params map {"action_input":"1"}？不，
	// rawInputToParams 解析 input "1" → 这里 input 本身是字符串 "1"，参数为注入原始串。
	// okTool.Execute 返回 "ok(1)"。断言 tool_result 含 "ok(1)"。
	if _, err := e.Run(AgentContext{TenantID: 7, UserID: 42, SessionID: "123"}, "今年销售额多少？"); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	msgs := sink.wait(4)
	if len(msgs) != 4 {
		t.Fatalf("冷轨应收到 4 条完整原文(提问/工具指令/工具结果/回答), got %d: %+v", len(msgs), msgs)
	}

	// 顺序 + 通用字段
	wantRoles := []string{"user", "tool", "tool", "assistant"}
	wantKinds := []string{"question", "tool_call", "tool_result", "answer"}
	for i, m := range msgs {
		if m.Role != wantRoles[i] {
			t.Errorf("第 %d 条 role 期望 %q, got %q", i, wantRoles[i], m.Role)
		}
		if m.Kind != wantKinds[i] {
			t.Errorf("第 %d 条 kind 期望 %q, got %q", i, wantKinds[i], m.Kind)
		}
		if m.TenantID != 7 || m.UserID != 42 {
			t.Errorf("第 %d 条 tenant/user 应透传 (7,42), got (%d,%d)", i, m.TenantID, m.UserID)
		}
		if m.SessionID != "123" {
			t.Errorf("第 %d 条 session 应透传 123, got %q", i, m.SessionID)
		}
	}

	// 内容要点
	if msgs[0].Content != "今年销售额多少？" {
		t.Errorf("question 内容错误: %q", msgs[0].Content)
	}
	if !strings.HasPrefix(msgs[1].Content, "ok_tool") {
		t.Errorf("tool_call 应以工具名开头: %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[2].Content, "ok(1)") {
		t.Errorf("tool_result 应含执行结果全文: %q", msgs[2].Content)
	}
	if msgs[3].Content != "销售额是1200万。" {
		t.Errorf("answer 内容错误: %q", msgs[3].Content)
	}

	// 冷轨工具结果为完整原文（不应被截断/摘要化）：长度应远大于热轨模板摘要
	if !strings.Contains(msgs[2].Content, "ok(1)") {
		t.Fatalf("冷轨应保留工具结果全文")
	}
}

// TestPersistFullHistory_NilSinkSkipsColdTrack
// 未注入 sink（nil）时冷轨整条链路静默跳过，不影响热轨对话（Memory 仍写 user/tool摘要/assistant）。
func TestPersistFullHistory_NilSinkSkipsColdTrack(t *testing.T) {
	llm := &captureLLM{
		replies: []string{
			`{"action":"ok_tool","action_input":"1"}`,
			`{"action":"final_answer","action_input":"已完成。"}`,
		},
	}
	tm := toolmanager.NewToolManager()
	_ = tm.RegisterTool(okTool{})
	mem := memory.NewInMemoryMemory()
	e := NewReActEngine(llm, tm, mem, "你是一个助手")
	// 不注入 sink

	if _, err := e.Run(AgentContext{TenantID: 1, SessionID: "no-sink"}, "查一下"); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	// 热轨应正常写了 history（含工具模板摘要），证明 nil sink 不影响主流程
	hist := mem.GetHistory(1, "no-sink")
	if len(hist) != 3 {
		t.Fatalf("无 sink 时热轨仍应写 user+工具摘要+assistant 3 条, got %d: %+v", len(hist), hist)
	}
}
