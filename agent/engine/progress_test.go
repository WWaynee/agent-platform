package engine

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestProgress_EventSequence 验证全流程流式进度事件序列（需求单 0009）：
// 一次"调工具 → 最终回答"的对话，应依次发出
//   thinking → tool_call → tool_result → thinking → answer_text → done
// 且 done 携带完整最终回答、工具调用清单、会话 ID。
func TestProgress_EventSequence(t *testing.T) {
	// 第1轮调 ok_tool，第2轮 final_answer
	llm := &sequenceLLM{
		replies: []string{
			`{"action":"ok_tool","action_input":"hello"}`,
			`{"action":"final_answer","action_input":"最终回答内容"}`,
		},
	}
	e := newTestEngineWith(llm, okTool{})

	var types []ProgressEventType
	var doneAnswer string
	var doneTools []string
	var doneSession string

	pf := func(ev ProgressEvent) {
		types = append(types, ev.Type)
		switch ev.Type {
		case ProgressDone:
			doneAnswer = ev.Answer
			doneTools = ev.ToolCalls
			doneSession = ev.SessionID
		}
	}

	// RunWithProgress 以单次调用参数传进度回调（验证 per-call 事件）
	_, err := e.RunWithProgress(AgentContext{TenantID: 1, SessionID: "p-seq"}, "请调用工具并回答", pf)
	if err != nil {
		t.Fatalf("Run 不应报错: %v", err)
	}

	// 事件类型序列（不含具体内容）
	var got []string
	for _, ty := range types {
		got = append(got, string(ty))
	}
	want := []string{
		string(ProgressThinking),  // 第1轮思考
		string(ProgressToolCall),  // 调 ok_tool
		string(ProgressToolResult),// ok_tool 返回
		string(ProgressThinking),  // 第2轮思考
		string(ProgressAnswerText),// 最终回答
		string(ProgressDone),      // 结束
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列不匹配\n got: %v\nwant: %v", got, want)
	}

	// done 事件数据
	if doneAnswer != "最终回答内容" {
		t.Errorf("done.answer = %q, want 最终回答内容", doneAnswer)
	}
	if len(doneTools) != 1 || doneTools[0] != "ok_tool" {
		t.Errorf("done.tool_calls = %v, want [ok_tool]", doneTools)
	}
	if doneSession != "p-seq" {
		t.Errorf("done.session_id = %q, want p-seq", doneSession)
	}
}

// TestProgress_NilCallback 未注入回调时引擎照常工作，不 panic（向后兼容）。
func TestProgress_NilCallback(t *testing.T) {
	llm := &sequenceLLM{replies: []string{`{"action":"final_answer","action_input":"ok"}`}}
	e := newTestEngineWith(llm, okTool{})
	// 不 SetProgress
	resp, err := e.Run(AgentContext{TenantID: 1, SessionID: "p-nil"}, "hi")
	if err != nil {
		t.Fatalf("Run 不应报错: %v", err)
	}
	if resp.Answer != "ok" {
		t.Fatalf("无回调时也应正常返回, got %q", resp.Answer)
	}
}

// TestProgress_ConcurrentIsolation 并发回归（需求单 0009-修复）：
// RunWithProgress 以「单次调用参数」传回调，多个并发请求各自收到自己的事件、互不串流。
// （曾用引擎字段 SetProgress 会被并发覆盖，导致事件写到别的请求/死连接。）
func TestProgress_ConcurrentIsolation(t *testing.T) {
	const n = 8

	doneCh := make(chan struct{})
	errCh := make(chan error, n)

	launch := func(i int) {
		var got []ProgressEventType
		sid := "c-" + strconv.Itoa(i)
		// 每个 goroutine 独立 sequenceLLM：sequenceLLM.call 无锁，共享则 -race 数据竞争
		llm := &sequenceLLM{replies: []string{fmt.Sprintf(`{"action":"final_answer","action_input":"ok-%d"}`, i)}}
		eng := newTestEngineWith(llm, okTool{})
		_, err := eng.RunWithProgress(AgentContext{TenantID: 1, SessionID: sid}, "hi", func(ev ProgressEvent) {
			got = append(got, ev.Type)
			if ev.Type == ProgressDone && ev.SessionID != sid {
				errCh <- fmt.Errorf("请求 %d done 事件串流: 事件 session=%q 期望 %q", i, ev.SessionID, sid)
			}
		})
		if err != nil {
			errCh <- err
			return
		}
		// 每个请求的事件序列应相同：thinking → answer_text → done
		if len(got) != 3 || got[0] != ProgressThinking || got[1] != ProgressAnswerText || got[2] != ProgressDone {
			errCh <- fmt.Errorf("请求 %d 事件序列异常: %v", i, got)
			return
		}
		doneCh <- struct{}{}
	}
	for i := 0; i < n; i++ {
		go launch(i)
	}
	for i := 0; i < n; i++ {
		select {
		case <-doneCh:
		case err := <-errCh:
			t.Fatalf("并发请求出错: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("并发请求超时")
		}
	}
}
