package api

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"agent-platform/api/handler"
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

	// 业务路由组（租户/用户接口先放公开路由组，方便调试；接入 JWT 后再移到私有组）
	business := r.Group("/api")
	{
		// 租户管理
		business.POST("/tenant", handler.CreateTenant)                 // 创建租户
		business.GET("/tenant/list", handler.ListTenants)              // 租户列表
		business.GET("/tenant/:id", handler.GetTenantDetail)           // 租户详情
		business.PUT("/tenant/:id/status", handler.UpdateTenantStatus) // 更新状态

		// 用户注册 / 登录（放公开路由组：未登录才能调用，登录后不再需要）
		business.POST("/user/register", handler.Register) // 用户注册
		business.POST("/user/login", handler.Login)       // 用户登录
	}

	// 私有路由组占位（后续接入 JWT 鉴权）
	private := r.Group("/api")
	{
		_ = private
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
