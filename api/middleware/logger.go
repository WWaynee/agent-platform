package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger 请求日志中间件
// 为每个请求生成并注入 trace_id（写满一周后需配合 trace 中间件与全链路日志）
// 记录请求方法、路径、状态码、耗时
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		// 处理业务
		c.Next()

		// 请求结束，记录日志
		cost := time.Since(start)
		traceID, _ := c.Get("trace_id")
		status := c.Writer.Status()

		fmt.Printf("[HTTP] %s | %-5s | %-3d | %s | %v | trace_id=%v\n",
			time.Now().Format("2006-01-02 15:04:05"),
			c.Request.Method,
			status,
			path,
			cost,
			traceID,
		)
	}
}
