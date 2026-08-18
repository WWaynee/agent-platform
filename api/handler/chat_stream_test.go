package handler

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"agent-platform/agent/engine"
)

// httptestRecorder wrap 成可按 sseWriter 方式使用的 gin.ResponseWriter。
// 用 httptest.NewRecorder（实现 http.Flusher）+ gin.ResponseWriter 适配。
type flusherRecorder struct {
	*httptest.ResponseRecorder
}

// Flush 实现 http.Flusher 空实现（ResponseRecorder 已实现；这里显式满足接口）。
func (f *flusherRecorder) Flush() {
	// no-op，ResponseRecorder 内部已收集
}

// TestSSEWriter_FullFlow 验证 sseWriter 能把引擎的全流程进度事件
// 转成正确的 SSE 文本（event:/data:/\n\n），并逐字输出 answer_token、done 收尾。
func TestSSEWriter_FullFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	// 用 gin 创建最小 Context，其 Writer 需支持 http.Flusher
	c, _ := gin.CreateTestContext(rec)
	// gin.CreateTestContext 的 writer 是我们传入的 ResponseRecorder（本身实现 Flusher）
	// 但我们 sseWriter.enable 断言 http.Flusher；ResponseRecorder 实现 Flush（满足 http.Flusher）

	sw := &sseWriter{c: c}
	if !sw.enable() {
		t.Fatalf("expect enable(): %T 应实现 http.Flusher", c.Writer)
	}

	// 注入 progressToSSE 并模拟引擎事件
	pf := sw.progressToSSE("session-42")
	pf(engine.ProgressEvent{Type: engine.ProgressThinking, Message: "正在思考…"})
	pf(engine.ProgressEvent{Type: engine.ProgressToolCall, ToolName: "knowledge_retrieve", Message: "正在调用 knowledge_retrieve 工具…"})
	pf(engine.ProgressEvent{Type: engine.ProgressToolResult, ToolName: "knowledge_retrieve", Result: "片段1"})
	pf(engine.ProgressEvent{Type: engine.ProgressAnswerText, Text: "你好啊"})
	pf(engine.ProgressEvent{Type: engine.ProgressDone, Answer: "你好啊", ToolCalls: []string{"knowledge_retrieve"}, SessionID: "session-42"})

	out := rec.Body.String()
	// 断言关键事件与数据
	checks := []string{
		"event: thinking",
		"event: tool_call",
		`"tool":"knowledge_retrieve"`,
		"event: tool_result",
		`"result":"片段1"`,
		"event: answer_token",
		`"delta":"你"`,
		`"delta":"好"`,
		"event: done",
		`"answer":"你好啊"`,
		`"session_id":"session-42"`,
	}
	for _, cst := range checks {
		if !bytes.Contains([]byte(out), []byte(cst)) {
			t.Errorf("SSE 输出缺少关键片段 %q\n--- 输出 ---\n%s", cst, out)
		}
	}
}

// TestSSEWriter_NoProgress 不触发进度时，不应写出错误帧。
func TestSSEWriter_NoProgress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	sw := &sseWriter{c: c}
	sw.enable()
	if rec.Body.Len() != 0 {
		t.Fatalf("无事件时不应有输出，got %q", rec.Body.String())
	}
}
