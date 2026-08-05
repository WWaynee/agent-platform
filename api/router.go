package api

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
)

// NewRouter 初始化 Gin 引擎并注册所有路由
func NewRouter() *gin.Engine {
	r := gin.New()

	// 全局中间件（顺序：Trace → Recovery → Logger → CORS）
	r.Use(
		middleware.Trace(),     // 生成 trace_id，注入 context
		middleware.Recovery(),  // panic 恢复
		middleware.Logger(),    // 请求日志
		cors.Default(),         // 跨域
	)

	// 公开路由组（无需鉴权）
	public := r.Group("/api")
	{
		// 健康检查
		public.GET("/health", handleHealth)
	}

	// 私有路由组（需 JWT 鉴权，后续阶段接入）
	private := r.Group("/api")
	// private.Use(middleware.Auth())  // 后续阶段接入
	{
		_ = private // 占位，后续补充私有接口
	}

	return r
}

// handleHealth 健康检查接口
// 返回服务存活状态
func handleHealth(c *gin.Context) {
	response.Success(c, gin.H{
		"status": "ok",
		"time":   nowStr(),
	})
}

// nowStr 返回当前时间的字符串格式
func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
