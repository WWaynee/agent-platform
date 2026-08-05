package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
)

// Recovery 全局异常恢复中间件
// 捕获请求处理过程中的 panic，记录日志，统一返回 500，避免服务崩溃
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 1. 打印错误日志 + 堆栈
				traceID, _ := c.Get("trace_id")
				stack := debug.Stack()
				fmt.Printf("[Recovery] %s | trace_id=%v | %s %s | panic: %v\n%s\n",
					time.Now().Format("2006-01-02 15:04:05"),
					traceID,
					c.Request.Method,
					c.Request.URL.Path,
					err,
					stack,
				)

				// 2. 统一返回 500，不让服务崩溃
				// 先中断后续处理，避免继续执行已 panic 的 handler
				c.AbortWithStatusJSON(http.StatusInternalServerError, response.Body{
					Code:    response.CodeServerError,
					Message: "服务器内部错误",
					Data:    nil,
				})
			}
		}()

		c.Next()
	}
}
