package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ============ Prometheus 指标（云原生监控） ============
//
// 为什么用 Prometheus：
//   - 云原生标准的监控方案（CNCF 项目），K8s/主流云平台内置支持
//   - 指标可按维度聚合/打点，做图表（请求量趋势、延迟分布、成功率）与告警
//   - 面试能讲"生产级监控"：拉模型（/metrics 暴露）+ 结构化指标 + 查询/告警
//
// 统一入口：本包用 promauto 注册（进程内全局唯一），保证重复 Import 不重复注册 panic。
// 使用方在各层打点即可，无需手动初始化。生产部署由服务暴露 /metrics 端点给 Prometheus 抓取。
//
// 指标清单（按 README 周四"接入 Prometheus 指标"规划）：
//   - http_requests_total              HTTP 请求总数（Counter，method/path/status_code）
//   - http_request_duration_seconds    HTTP 请求耗时（Histogram，method/path/status_code）
//   - llm_calls_total                  LLM 调用总数（Counter，model/success）
//   - llm_tokens_total                 LLM token 消耗总数（Counter，model）
//   - tool_calls_total                 工具调用总数（Counter，tool_name/success）
//   - mq_messages_total                MQ 消息处理数（Counter，queue/status）

// 各层可通过这些"标签构造器"统一拼维度，避免各处手写字符串拼错。

// ============ 1. HTTP 请求指标 ============

// HTTPRequestsTotal HTTP 请求总数。标签：method / path / status_code。
var HTTPRequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests handled by the API.",
	},
	[]string{"method", "path", "status_code"},
)

// HTTPRequestDuration HTTP 请求耗时（秒）。标签：method / path / status_code。
var HTTPRequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests handled by the API, in seconds.",
		Buckets: prometheus.DefBuckets, // 默认桶：0.005~10s，覆盖常规接口耗时
	},
	[]string{"method", "path", "status_code"},
)

// ============ 2. LLM 指标 ============

// LLMCallsTotal LLM 调用总数。标签：model / success("true"/"false")。
var LLMCallsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "llm_calls_total",
		Help: "Total number of LLM calls made, labeled by model and success.",
	},
	[]string{"model", "success"},
)

// LLMTokensTotal LLM token 消耗总数。标签：model。
var LLMTokensTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "llm_tokens_total",
		Help: "Total number of tokens consumed by LLM calls, labeled by model.",
	},
	[]string{"model"},
)

// ============ 3. 工具调用指标 ============

// ToolCallsTotal 工具调用总数。标签：tool_name / success("true"/"false")。
var ToolCallsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "tool_calls_total",
		Help: "Total number of agent tool calls, labeled by tool name and success.",
	},
	[]string{"tool_name", "success"},
)

// ============ 4. MQ 消息指标 ============

// MQMessagesTotal MQ 消息处理数。标签：queue / status("published"/"ack"/"nack"/"requeue"/"error")。
var MQMessagesTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "mq_messages_total",
		Help: "Total number of MQ messages handled, labeled by queue and processing status.",
	},
	[]string{"queue", "status"},
)

// ============ 便捷打点辅助（把 success bool 规范化成标签值，避免各处写字符串） ============

// successLabel 把 bool 转成 prometheus 标签值（"true"/"false"），统一约定。
func successLabel(ok bool) string {
	if ok {
		return "true"
	}
	return "false"
}

// IncHTTPRequest 便捷打点：HTTP 请求总数 + 耗时（耗时单位为秒，由调用方换算）。
func IncHTTPRequest(method, path, statusCode string, durationSeconds float64) {
	HTTPRequestsTotal.With(prometheus.Labels{"method": method, "path": path, "status_code": statusCode}).Inc()
	HTTPRequestDuration.With(prometheus.Labels{"method": method, "path": path, "status_code": statusCode}).Observe(durationSeconds)
}

// IncLLMCall 便捷打点：LLM 调用次数（model / 是否成功）。
func IncLLMCall(model string, success bool) {
	LLMCallsTotal.With(prometheus.Labels{"model": model, "success": successLabel(success)}).Inc()
}

// AddLLMTokens 便捷打点：LLM token 消耗（model / 数量）。
func AddLLMTokens(model string, tokenCount float64) {
	LLMTokensTotal.With(prometheus.Labels{"model": model}).Add(tokenCount)
}

// IncToolCall 便捷打点：工具调用次数（tool_name / 是否成功）。
func IncToolCall(toolName string, success bool) {
	ToolCallsTotal.With(prometheus.Labels{"tool_name": toolName, "success": successLabel(success)}).Inc()
}

// IncMQMessage 便捷打点：MQ 消息处理数（queue / 状态）。
func IncMQMessage(queue, status string) {
	MQMessagesTotal.With(prometheus.Labels{"queue": queue, "status": status}).Inc()
}
