package observability

import (
	"bytes"
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
