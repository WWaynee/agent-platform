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

	// 公开路由组（无需登录，可直接访问）
	public := r.Group("/api")
	{
		// 健康检查（无 /api 前缀，放根路径）
		r.GET("/health", handleHealth)

		// 用户注册 / 登录（未登录才能调用）
		public.POST("/user/register", handler.Register) // 用户注册
		public.POST("/user/login", handler.Login)       // 用户登录
	}

	// 私有路由组（挂 JWT 鉴权中间件，必须带有效 token 才能访问）
	private := r.Group("/api")
	private.Use(middleware.JWTAuth())
	{
		// 测试接口：从 Context 拿当前登录用户信息
		private.GET("/user/info", handler.GetUserInfo)

		// 文档上传（需登录，tenant_id 从 JWT 上下文拿）
		private.POST("/document/upload", handler.UploadDocument)

		// 租户管理（创建/查询租户都需登录）
		private.POST("/tenant", handler.CreateTenant)                 // 创建租户
		private.GET("/tenant/list", handler.ListTenants)              // 租户列表
		private.GET("/tenant/:id", handler.GetTenantDetail)           // 租户详情
		private.PUT("/tenant/:id/status", handler.UpdateTenantStatus) // 更新状态
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
