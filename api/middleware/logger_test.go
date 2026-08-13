package middleware

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

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

// TestLogger_PrometheusMetric 验证：发一次请求后，http_requests_total +1、
// http_request_duration_seconds 记录耗时，且 method/path/status_code 维度标签正确。
func TestLogger_PrometheusMetric(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = observability.InitWith(&bytes.Buffer{}, "info")

	engine := gin.New()
	engine.Use(Logger())
	engine.GET("/api/metrics-check", func(c *gin.Context) {
		c.Status(200)
	})
	engine.POST("/api/metrics-check", func(c *gin.Context) {
		c.Status(400)
	})

	// 记录打点前的基数
	before := counterValue(t, "http_requests_total",
		prometheus.Labels{"method": "GET", "path": "/api/metrics-check", "status_code": "200"})

	// 发 2 次 GET 200 + 1 次 POST 400
	performRequest(engine, "GET", "/api/metrics-check")
	performRequest(engine, "GET", "/api/metrics-check")
	performRequest(engine, "POST", "/api/metrics-check")

	// GET/200 应为 +2
	got := counterValue(t, "http_requests_total",
		prometheus.Labels{"method": "GET", "path": "/api/metrics-check", "status_code": "200"})
	if got-before != 2 {
		t.Errorf("HTTP GET/200 计数应 +2，实际 %v→%v", before, got)
	}
	// POST/400 首次出现应为 +1
	gotPost := counterValue(t, "http_requests_total",
		prometheus.Labels{"method": "POST", "path": "/api/metrics-check", "status_code": "400"})
	if gotPost != 1 {
		t.Errorf("HTTP POST/400 计数应 =1，实际 %v", gotPost)
	}
	// 耗时 histogram 的 sample_count 也应等于请求数（GET/200 应为 2）
	histCount := counterValue(t, "http_request_duration_seconds",
		prometheus.Labels{"method": "GET", "path": "/api/metrics-check", "status_code": "200"})
	if histCount != 2 {
		t.Errorf("histogram sample_count 应 =2，实际 %v", histCount)
	}

	t.Logf("✅ HTTP 埋点生效：GET/200=+2、POST/400=+1，耗时 histogram sample_count=2，维度标签(method/path/status_code)正确")
}

// counterValue 从默认注册表读取指定指标（counter value 或 histogram sample_count）
// 在给定标签集合下的值；找不到该标签组合返回 0。
func counterValue(t *testing.T, name string, labels prometheus.Labels) float64 {
	t.Helper()
	gather, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("收集指标失败: %v", err)
	}
	for _, mf := range gather {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, lp := range m.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			match := true
			for k, v := range labels {
				if got[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			if mc := m.GetCounter(); mc != nil {
				return mc.GetValue()
			}
			if mh := m.GetHistogram(); mh != nil {
				return float64(mh.GetSampleCount())
			}
		}
	}
	return 0
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
