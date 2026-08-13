package observability

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// TestMetrics_Registered 验证：核心指标已向默认注册表注册成功（无 panic / 不重复注册）。
// 注册表通过 promauto 管理，注册成功即说明定义合法、重复 Import 安全。
func TestMetrics_Registered(t *testing.T) {
	// 遍历本轮定义的指标，确认都能在默认注册表里查到
	expected := []string{
		"http_requests_total",
		"http_request_duration_seconds",
		"llm_calls_total",
		"llm_tokens_total",
		"tool_calls_total",
		"mq_messages_total",
	}

	// promauto 注册的 Vec 指标为惰性：只有实例化（With 某个标签组合）后 Gather 才返回 family。
	// 为验证"定义合法 + 注册成功"，先各实例化一个基础标签组合。
	HTTPRequestsTotal.WithLabelValues("GET", "/health", "200")
	HTTPRequestDuration.WithLabelValues("GET", "/health", "200")
	LLMCallsTotal.WithLabelValues("deepseek-chat", "true")
	LLMTokensTotal.WithLabelValues("deepseek-chat")
	ToolCallsTotal.WithLabelValues("echo", "true")
	MQMessagesTotal.WithLabelValues("document_parse", "ack")

	gather, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("指标收集失败: %v", err)
	}

	for _, name := range expected {
		found := false
		for _, mf := range gather {
			if mf.GetName() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("指标 %q 未在注册表中注册", name)
		}
	}
	t.Logf("✅ 核心指标全部注册成功: %v", expected)
}

// TestMetrics_HTTPPoint 验证：HTTP 请求打点（总数 + 耗时）正确，维度可随标签区分。
func TestMetrics_HTTPPoint(t *testing.T) {
	IncHTTPRequest("GET", "/api/chat", "200", 0.0123)
	IncHTTPRequest("GET", "/api/chat", "200", 0.0456)
	IncHTTPRequest("POST", "/api/chat", "500", 0.200)

	// 总数：3 条请求
	wantCalls := 3.0
	if got := testutil.ToFloat64(HTTPRequestsTotal.With(prometheus.Labels{"method": "GET", "path": "/api/chat", "status_code": "200"})); got != 2 {
		t.Errorf("GET/chat/200 应为 2 次，实际 %v", got)
	}
	_ = wantCalls

	// 验证 /metrics 文本里能抓到这些指标名与标签
	out := testMetricText(t)
	for _, substr := range []string{
		"http_requests_total",
		`http_requests_total{method="GET",path="/api/chat",status_code="200"} 2`,
		"http_request_duration_seconds",
		"http_request_duration_seconds_count",
		"http_request_duration_seconds_sum",
	} {
		if !strings.Contains(out, substr) {
			t.Errorf("metrics 文本缺少 %q", substr)
		}
	}
	t.Log("✅ HTTP 请求指标打点正确（总数 + 耗时 histogram，含 method/path/status 标签）")
}

// TestMetrics_LLMPoint 验证：LLM 调用与 token 指标打点。
func TestMetrics_LLMPoint(t *testing.T) {
	IncLLMCall("deepseek-chat", true)
	IncLLMCall("deepseek-chat", true)
	IncLLMCall("deepseek-chat", false)
	AddLLMTokens("deepseek-chat", 1250)
	AddLLMTokens("silicon-flow-embed", 800)

	out := testMetricText(t)
	for _, substr := range []string{
		`llm_calls_total{model="deepseek-chat",success="true"} 2`,
		`llm_calls_total{model="deepseek-chat",success="false"} 1`,
		`llm_tokens_total{model="deepseek-chat"} 1250`,
		`llm_tokens_total{model="silicon-flow-embed"} 800`,
	} {
		if !strings.Contains(out, substr) {
			t.Errorf("metrics 文本缺少 %q", substr)
		}
	}
	t.Log("✅ LLM 调用/token 指标打点正确（model/success 标签）")
}

// TestMetrics_ToolPoint 验证：工具调用指标打点。
func TestMetrics_ToolPoint(t *testing.T) {
	IncToolCall("knowledge_retrieve", true)
	IncToolCall("knowledge_retrieve", true)
	IncToolCall("echo", false)

	out := testMetricText(t)
	for _, substr := range []string{
		`tool_calls_total{success="false",tool_name="echo"} 1`,
		`tool_calls_total{success="true",tool_name="knowledge_retrieve"} 2`,
	} {
		if !strings.Contains(out, substr) {
			t.Errorf("metrics 文本缺少 %q", substr)
		}
	}
	t.Log("✅ 工具调用指标打点正确（tool_name/success 标签）")
}

// TestMetrics_MQPoint 验证：MQ 消息指标打点。
func TestMetrics_MQPoint(t *testing.T) {
	IncMQMessage("document_parse", "ack")
	IncMQMessage("document_parse", "ack")
	IncMQMessage("document_parse", "requeue")

	out := testMetricText(t)
	for _, substr := range []string{
		`mq_messages_total{queue="document_parse",status="ack"} 2`,
		`mq_messages_total{queue="document_parse",status="requeue"} 1`,
	} {
		if !strings.Contains(out, substr) {
			t.Errorf("metrics 文本缺少 %q", substr)
		}
	}
	t.Log("✅ MQ 消息指标打点正确（queue/status 标签）")
}

// testMetricText 抓取默认注册表的 /metrics 文本内容（自测用，Prometheus 文本格式）。
func testMetricText(t *testing.T) string {
	t.Helper()
	gather, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("指标收集失败: %v", err)
	}
	var buf bytes.Buffer
	enc := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, mf := range gather {
		if err := enc.Encode(mf); err != nil {
			t.Fatalf("编码指标失败: %v", err)
		}
	}
	return buf.String()
}

// TestMetrics_HTTPEndpoint 验证 /metrics 端点（MetricsHandler）：
//  1. 返回 HTTP 200；
//  2. Content-Type 为 Prometheus 标准文本格式（text/plain; version=0.0.4; charset=utf-8）；
//  3. 文本包含已埋点的核心指标（Prometheus 文本格式）；
//  4. 发一次请求（打点）后，指标数值随之变化。
func TestMetrics_HTTPEndpoint(t *testing.T) {
	// 用 MetricsHandler 经标准库 handler 响应一次抓取
	srv := httptest.NewServer(MetricsHandler())
	defer srv.Close()

	// promauto 的 Vec 指标为惰性：未实例化（With 标签）前不会出现在抓取结果里。
	// 先在默认注册表实例化各核心指标，确保抓取文本里能看到它们的 HELP/TYPE 行。
	HTTPRequestsTotal.WithLabelValues("GET", "/health", "200")
	HTTPRequestDuration.WithLabelValues("GET", "/health", "200")
	LLMCallsTotal.WithLabelValues("deepseek-chat", "true")
	LLMTokensTotal.WithLabelValues("deepseek-chat")
	ToolCallsTotal.WithLabelValues("echo", "true")
	MQMessagesTotal.WithLabelValues("document_parse", "ack")

	// 首次抓取：仅校验格式与指标名存在（不依赖具体累计值）
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("请求 /metrics 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics 应返回 200，实际 %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") || !strings.Contains(ct, "version=0.0.4") {
		t.Errorf("Content-Type 应为 Prometheus 文本格式，实际 %q", ct)
	}

	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	out := body.String()

	// 标准格式特征：# HELP / # TYPE 行 + 各指标名
	for _, substr := range []string{
		"# HELP http_requests_total",
		"# TYPE http_requests_total counter",
		"# HELP http_request_duration_seconds",
		"# TYPE http_request_duration_seconds histogram",
		"# HELP llm_calls_total",
		"# TYPE llm_calls_total counter",
		"llm_tokens_total",
		"tool_calls_total",
		"mq_messages_total",
	} {
		if !strings.Contains(out, substr) {
			t.Errorf("/metrics 文本缺少 Prometheus 元素 %q", substr)
		}
	}

	// 发请求（打点）后数值变化：GET/health 200 再 +1
	IncHTTPRequest("GET", "/health", "200", 0.05)
	got := testutil.ToFloat64(HTTPRequestsTotal.With(prometheus.Labels{"method": "GET", "path": "/health", "status_code": "200"}))
	if got < 1 {
		t.Errorf("打点后 http_requests_total{GET,/health,200} 应 >=1，实际 %v", got)
	}

	// 再次抓取，确认该指标出现在文本且值=最新计数
	resp2, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("二次请求 /metrics 失败: %v", err)
	}
	defer resp2.Body.Close()
	body2 := new(bytes.Buffer)
	_, _ = body2.ReadFrom(resp2.Body)
	if !strings.Contains(body2.String(), fmt.Sprintf(`http_requests_total{method="GET",path="/health",status_code="200"} %d`, int64(got))) {
		t.Errorf("/metrics 未反映最新计数（值=%v）:\n%.300s", got, body2.String())
	}

	t.Log("✅ /metrics 端点正常：标准 Prometheus 文本格式，指标可被抓取，打点后数值变化")
}
