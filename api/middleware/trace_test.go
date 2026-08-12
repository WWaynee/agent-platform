package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"agent-platform/agent/interfaces"
)

// headerPerformRequest 发起一次 gin 请求，可指定请求头，返回 recorder。
func headerPerformRequest(engine *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// bodyTraceID 返回一个把 ctx 中 trace_id 写入响应体的测试路由。
func newTraceEngine() (*gin.Engine, *[]string) {
	var captured []string
	engine := gin.New()
	// 顺序：Trace 先生成 trace_id，内层 handler 从请求级 context 读它验证"context 里能拿到"。
	engine.Use(Trace())
	engine.GET("/ping", func(c *gin.Context) {
		tid := interfaces.TraceIDFromCtx(c.Request.Context())
		captured = append(captured, tid)
		c.String(200, tid)
	})
	return engine, &captured
}

// TestTrace_EveryRequestUniqueTraceID 自测①：每个请求都拿到唯一 trace_id，且两次请求不同。
func TestTrace_EveryRequestUniqueTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine, captured := newTraceEngine()

	set := map[string]bool{}
	for i := 0; i < 20; i++ {
		w := headerPerformRequest(engine, "GET", "/ping", nil)
		tid := w.Body.String()
		if tid == "" {
			t.Fatalf("第 %d 次请求 ctx 中应能取到 trace_id，实际为空", i)
		}
		if set[tid] {
			t.Fatalf("trace_id 重复: %s", tid)
		}
		set[tid] = true
	}
	if len(*captured) != 20 {
		t.Errorf("handler 应从 ctx 取到 trace_id 共 20 次，实际 %d", len(*captured))
	}
	t.Log("✅ 每个请求 trace_id 唯一，且 context 能取到")
}

// TestTrace_ResponseHeader 自测③：响应头带 X-Trace-Id，且与 ctx 中一致。
func TestTrace_ResponseHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine, captured := newTraceEngine()
	w := headerPerformRequest(engine, "GET", "/ping", nil)

	respHeader := w.Header().Get(HeaderTraceID)
	if respHeader == "" {
		t.Fatal("响应头应包含 X-Trace-Id")
	}
	if len(*captured) == 0 {
		t.Fatal("handler 未取到 ctx trace_id")
	}
	if respHeader != (*captured)[0] {
		t.Errorf("响应头 X-Trace-Id(%s) 应与 ctx 中 trace_id(%s) 一致", respHeader, (*captured)[0])
	}
	t.Log("✅ 响应头带 X-Trace-Id 且与 context 一致")
}

// TestTrace_PassthroughUpstream 自测④：请求头带合法 X-Trace-Id 时应沿用（不重新生成）。
func TestTrace_PassthroughUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine, captured := newTraceEngine()
	w := headerPerformRequest(engine, "GET", "/ping", map[string]string{HeaderTraceID: "upstream-trace-abc"})

	// context 里 & 响应头都应沿用上游值
	if (*captured)[0] != "upstream-trace-abc" {
		t.Errorf("应沿用上游 trace_id=%q，实际 context=%q", "upstream-trace-abc", (*captured)[0])
	}
	if got := w.Header().Get(HeaderTraceID); got != "upstream-trace-abc" {
		t.Errorf("响应头应回写上游 value=%q，实际 %q", "upstream-trace-abc", got)
	}
	t.Log("✅ 上游 X-Trace-Id 被透传沿用")
}

// TestTrace_RejectInvalidUpstream 非法/超长上游 trace_id 被摒弃，改用新生成。
func TestTrace_RejectInvalidUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine, _ := newTraceEngine()

	for _, bad := range []string{"", "   ", strings.Repeat("a", 65), "with space", "ctrl\x01char", "111;drop"} {
		w := headerPerformRequest(engine, "GET", "/ping", map[string]string{HeaderTraceID: bad})
		tid := w.Header().Get(HeaderTraceID)
		if tid == "" {
			t.Fatalf("非法上游 %q 应回退为新生成的 trace_id，响应头却为空", bad)
		}
		if tid == bad {
			t.Errorf("非法上游 %q 不应被沿用", bad)
		}
	}
	t.Log("✅ 非法/超长上游 X-Trace-Id 被摒弃，回退生成新 trace_id")
}

// TestSanitizeTraceID 直接测清洗函数边界。
func TestSanitizeTraceID(t *testing.T) {
	if got := sanitizeTraceID("abc-123_def.456:xyz"); got != "abc-123_def.456:xyz" {
		t.Errorf("合法值应原样返回: %q", got)
	}
	if got := sanitizeTraceID(""); got != "" {
		t.Errorf("空串应返回空的: %q", got)
	}
	if got := sanitizeTraceID("hello world"); got != "" {
		t.Errorf("含空格应拒绝: %q", got)
	}
	if got := sanitizeTraceID(strings.Repeat("x", 65)); got != "" {
		t.Errorf("超长应拒绝: len=%d", len(got))
	}
	t.Log("✅ sanitizeTraceID 边界正确")
}
