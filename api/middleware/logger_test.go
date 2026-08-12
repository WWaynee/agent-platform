package middleware

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"agent-platform/agent/interfaces"
	"agent-platform/observability"
)

// TestLogger_RequestChain 验证：一次请求产生"收到请求"与"请求结束"两条结构化日志，
// 且都自动携带 trace_id / method / path / status_code / latency 规范字段。
func TestLogger_RequestChain(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 初始化全局 logger 到一个 buffer（中间件经 observability.WithContext 读取该 logger）
	var buf bytes.Buffer
	_ = observability.InitWith(&buf, "info")

	// 用内层 handler 模拟 JWTAuth：把 tenant/user 种进请求级标准 context
	// （真实链路 JWT 在 Logger 之后执行，c.Next() 结束时 Logger 能读到新 ctx）。
	engine := gin.New()
	engine.Use(Trace(), Logger())
	engine.GET("/api/health", func(c *gin.Context) {
		c.Request = c.Request.WithContext(interfaces.WithTenantUser(c.Request.Context(), 1001, 202))
		c.Status(200)
		c.String(200, `{"ok":true}`)
	})
	_ = performRequest(engine, "GET", "/api/health")

	out := buf.String()
	lines := splitLines(out)
	if len(lines) < 2 {
		t.Fatalf("应有至少 2 条日志（收到请求+请求结束），实际:\n%s", out)
	}

	// 记录"收到请求"与"请求结束"两段日志的字段
	var sawStart, sawEnd bool
	var startFields, endFields map[string]interface{}
	for _, line := range lines {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		msg, _ := m["msg"].(string)
		switch {
		case strings.Contains(msg, "收到请求"):
			sawStart = true
			startFields = m
		case strings.Contains(msg, "请求结束"):
			sawEnd = true
			endFields = m
		}
	}

	if !sawStart || !sawEnd {
		t.Fatalf("应同时出现收到请求/请求结束日志，实际:\n%s", out)
	}
	// 收到请求日志：应含 timestamp/method/path/client_ip + trace_id
	for _, k := range []string{"timestamp", "level", "msg", "trace_id", "method", "path"} {
		if v, ok := startFields[k]; !ok || v == nil {
			t.Errorf("收到请求日志缺少规范字段 %q: %v", k, startFields)
		}
	}
	// 请求结束日志：应含 trace_id + tenant_id + status_code + latency
	for _, k := range []string{"timestamp", "level", "msg", "trace_id", "tenant_id", "status_code", observability.FieldLatency} {
		if v, ok := endFields[k]; !ok || v == nil {
			t.Errorf("请求结束日志缺少规范字段 %q: %v", k, endFields)
		}
	}
	if endFields["tenant_id"] != float64(1001) {
		t.Errorf("请求结束日志 tenant_id 应为 1001，实际 %v", endFields["tenant_id"])
	}
	if endFields["status_code"] != float64(200) {
		t.Errorf("请求结束日志 status_code 应对 200，实际 %v", endFields["status_code"])
	}

	t.Logf("✅ 一次请求产生完整链路日志，请求结束日志: %s", findLine(lines, "请求结束"))
}

// findLine 返回包含关键字的日志行。
func findLine(lines []string, keyword string) string {
	for _, l := range lines {
		if strings.Contains(l, keyword) {
			return l
		}
	}
	return "<未找到>"
}

// performRequest 发起一次 gin 请求，返回响应 recorder。
func performRequest(engine *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// splitLines 按行切分输出并去掉空行。
func splitLines(out string) []string {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}
