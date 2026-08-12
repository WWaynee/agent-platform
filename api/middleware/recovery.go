package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"agent-platform/api/response"
	"agent-platform/observability"
)

// toError 把 panic 的 interface{} 归一成 error（error / string / 其他）
func toError(v interface{}) error {
	switch e := v.(type) {
	case error:
		return e
	case string:
		return fmt.Errorf("%s", e)
	default:
		return fmt.Errorf("%+v", e)
	}
}

// Recovery 全局异常恢复中间件
// 捕获请求处理过程中的 panic，记录日志，统一返回 500，避免服务崩溃
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 1. 记录 error 日志 + 堆栈（自动带 trace_id/tenant_id/user_id）
				observability.WithContext(c.Request.Context()).Error("请求 panic 已恢复",
					zap.Error(toError(err)),
					zap.String("method", c.Request.Method),
					zap.String("path", c.Request.URL.Path),
					zap.ByteString("stack", debug.Stack()),
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
