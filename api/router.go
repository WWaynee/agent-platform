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
		middleware.Trace(),    // 生成 trace_id，注入 context
		middleware.Recovery(), // panic 恢复
		middleware.Logger(),   // 请求日志
		cors.Default(),        // 跨域
	)

	// 公开路由组（无需鉴权）
	public := r.Group("")
	{
		// 健康检查
		public.GET("/health", handleHealth)
	}

	// 业务路由组
	business := r.Group("/api")
	{
		// 私有接口后续接 JWT 鉴权后放这里，当前先占位
		_ = business
	}

	return r
}

// AppVersion 服务版本号
const AppVersion = "v0.0.1"

// handleHealth 健康检查接口
// 返回服务存活状态 + 当前时间 + 版本号
func handleHealth(c *gin.Context) {
	response.Success(c, gin.H{
		"status":  "running",
		"time":    nowStr(),
		"version": AppVersion,
	})
}

// nowStr 返回当前时间的字符串格式
func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
