package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/gin-gonic/gin"

	"agent-platform/agent/interfaces"
)

// Trace 请求头/响应头用的 Trace ID 键名。
// ⚠️ 需与日志落库、前端联调、上游链路网关使用的键名保持一致。
const (
	HeaderTraceID = "X-Trace-Id" // 请求头（上游透传）与响应头（回给调用方）都用它
	maxTraceIDLen = 64           // 上限：拒绝超长/异常输入，避免脏 taint 污染日志与响应头
)

// Trace 全链路 TraceID 中间件
//
// 每个请求：
//  1. 若请求头带 `X-Trace-Id`（上游服务透传）且合法 → 沿用上游 trace_id（不重新生成）；
//  2. 否则生成一个新的全局唯一 trace_id；
//  3. 把 trace_id 写入标准 context（WithTraceID），供 observability.WithContext 全链路日志携带；
//  4. 把 trace_id 写进响应头 `X-Trace-Id`，方便前端/上游排查时对上同样的链路 ID。
//
// 为什么测透传：一次调用可能先经网关/上游再进本服务，若每次都重新生成，
// 同一业务链路两端 trace_id 对不上，无法串联排查。故优先采纳上游传入值。
func Trace() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 优先透传上游 X-Trace-Id（合法才采用，否则重新生成）
		traceID := sanitizeTraceID(c.GetHeader(HeaderTraceID))
		if traceID == "" {
			traceID = genTraceID()
		}

		// 2. 写进 context（gin 存一份 + 标准 context 存一份，供 WithContext 读取）
		c.Set("trace_id", traceID)
		c.Request = c.Request.WithContext(interfaces.WithTraceID(c.Request.Context(), traceID))

		// 3. 响应头回写，便于前端/上游定位同一链路
		c.Header(HeaderTraceID, traceID)

		c.Next()
	}
}

// sanitizeTraceID 校验并清洗上游传入的 trace_id：
//   - 空或空白 → 返回空串（调用方将重新生成）；
//   - 超长（>maxTraceIDLen）→ 返回空串（拒绝异常输入）；
//   - 允许 hex / UUID / 若干标点，含空白或控制字符 → 返回空串。
func sanitizeTraceID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxTraceIDLen {
		return ""
	}
	for _, r := range s {
		// 只允许字母、数字、常见分隔符（- _ . : 等），杜绝空白/控制字符
		if r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return ""
		}
	}
	return s
}

// genTraceID 生成 32 位十六进制随机 TraceID（等价 128bit UUID 的 hex 形态）。
func genTraceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极低概率失败，回退到固定前缀
		return "trace-fallback"
	}
	return hex.EncodeToString(b)
}
