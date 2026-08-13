package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"agent-platform/observability"
)

// Logger 请求日志中间件（结构化 JSON，统一字段规范）
// 记录请求入口（收到请求）与结束（状态码/耗时/错误）两段日志，
// 自动携带 trace_id / tenant_id / user_id（来自请求级 context，经 WithContext）。
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method
		clientIP := c.ClientIP()

		// 基于请求 context 派生带 trace_id/tenant_id/user_id 的 logger
		logger := observability.WithContext(c.Request.Context())

		// ① 请求入口日志：method / path / client_ip
		logger.Info("收到请求",
			zap.String("method", method),
			zap.String("path", path),
			zap.String("client_ip", clientIP),
		)

		// 处理业务
		c.Next()

		// ② 请求结束日志：status_code / latency / error（如有）
		//    请求结束时要重新基于『处理完后的 ctx』取 logger，
		//    因为链路内层（如 JWT/配额中间件）可能已把 tenant/user 种进 ctx，
		//    此时 logger 应携带这些身份字段，与 trace_id 一起定位到具体租户/用户。
		logger = observability.WithContext(c.Request.Context())

		status := c.Writer.Status()
		latency := time.Since(start)

		// Prometheus 指标埋点：请求总数 +1，耗时(秒)记入 histogram。
		// 标签只用 method/path/status_code 等低基数维度（不用 trace_id/user_id，防基数爆炸）。
		observability.IncHTTPRequest(method, path, fmt.Sprint(status), latency.Seconds())

		fields := []zap.Field{
			zap.String("method", method),
			zap.String("path", path),
			zap.Int("status_code", status),
			zap.Int64(observability.FieldLatency, latency.Milliseconds()),
		}
		if len(c.Errors) > 0 {
			// 有错误时记录 error 字段（zap.Error 自动带上 error 字段）
			fields = append(fields, zap.Error(c.Errors.Last().Err))
			logger.Error("请求结束（有错误）", fields...)
			return
		}
		logger.Info("请求结束", fields...)
	}
}
