package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// Trace 全链路 TraceID 中间件
// 为每个请求生成一个全局唯一的 trace_id，注入 context
// 后续 Logger、Recovery、结构化日志、审计日志都从 context 读取该值
func Trace() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 优先透传上游传入的 trace_id（如通过网关）？此处简化：每次生成新的
		traceID := genTraceID()
		c.Set("trace_id", traceID)
		c.Next()
	}
}

// genTraceID 生成 32 位十六进制随机 TraceID
func genTraceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极低概率失败，回退到固定前缀
		return "trace-fallback"
	}
	return hex.EncodeToString(b)
}
