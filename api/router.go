package api

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"agent-platform/api/handler"
	"agent-platform/api/middleware"
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
		// 返回所有依赖的健康状态：全部正常 200；任一依赖 down 返回 503（探针据此判定实例可用性）。
		r.GET("/health", handler.Health)

		// 用户注册 / 登录（未登录才能调用）
		public.POST("/user/register", handler.Register)         // 用户注册（校验租户存在）
		public.POST("/user/login", handler.Login)               // 用户登录（校验租户存在/启用）
		public.POST("/tenant/register", handler.RegisterTenant) // 注册租户（原子创建租户+首个admin+工具配置）
	}

	// 私有路由组（挂 JWT 鉴权中间件，必须带有效 token 才能访问）
	private := r.Group("/api")
	private.Use(middleware.JWTAuth())
	// 全局限流：所有私有接口都过租户级 + 用户级滑动窗口（分布式限流）
	private.Use(middleware.RateLimiter())
	{
		// 测试接口：从 Context 拿当前登录用户信息
		private.GET("/user/info", handler.GetUserInfo)

		// 文档上传（需登录，tenant_id 从 JWT 上下文拿）
		private.POST("/document/upload", handler.UploadDocument)
		// 文档分页列表（需登录，强制 tenant_id 过滤）
		private.GET("/document/list", handler.ListDocuments)
		// 文档详情（需登录，强制 tenant_id 过滤）
		private.GET("/document/:id", handler.GetDocumentDetail)
		// 文档删除（需登录，强制 tenant_id 过滤，删MinIO+软删DB）
		private.DELETE("/document/:id", handler.DeleteDocument)
		// 文档向量化触发（测试/调试接口，同步执行，触发切片→Embedding→写入Qdrant）
		private.POST("/document/:id/process", handler.ProcessDocument)

		// 文档下载/预览：返回 OSS 预签名 URL（需求单 0010）
		private.GET("/document/:id/url", handler.GetDocumentURL)
		// 文档内联预览：后端从 OSS 读回并返回 text/plain（保证浏览器内联展示，非下载）
		private.GET("/document/:id/preview", handler.PreviewDocument)

		// 异步任务状态查询（需登录，强制 tenant_id 过滤；前端轮询看处理进度）
		private.GET("/task/:id", handler.GetTask)

		// 知识库检索（测试/调试接口，按当前租户强制过滤检索文档片段）
		private.POST("/knowledge/search", handler.KnowledgeSearch)

		// Agent 对话（ReAct 引擎，需登录，tenant_id 从 JWT 上下文拿）
		// 对话接口叠加两层更严格的保护（调 LLM 成本高）：
		//   - ChatRateLimiter：对话专属限流（阈值更低，单独计数，见配置 RATE_LIMIT_CHAT_PER_MIN）
		//   - QuotaInterceptor：租户 token 配额拦截（超过 QuotaLlmToken 就拦截）
		private.POST("/chat", middleware.ChatRateLimiter(), middleware.QuotaInterceptor(), handler.Chat)

		// 会话管理（创建/列表/删除，需登录）
		private.POST("/session", handler.CreateSession)                  // 创建会话
		private.GET("/session/list", handler.GetSessionList)             // 会话列表（只当前用户）
		private.GET("/session/:id/messages", handler.GetSessionMessages) // 会话历史（只当前用户的会话；他租户/他人会话→无权）
		private.DELETE("/session/:id", handler.DeleteSession)            // 删除会话（只删自己的）

		// 租户查询/创建（需登录）
		private.POST("/tenant", handler.CreateTenant)       // 创建租户
		private.GET("/tenant/list", handler.ListTenants)    // 租户列表
		private.GET("/tenant/:id", handler.GetTenantDetail) // 租户详情

		// ===== 管理员专属路由组（JWT 先鉴权 → 再 AdminAuth，仅 admin 可调）=====
		admin := private.Group("/admin")
		admin.Use(middleware.AdminAuth())
		{
			// 工具配置查询/修改（租户管理员管理端）
			admin.GET("/tool-config", handler.GetToolConfigList)           // 当前租户所有工具开关状态
			admin.PUT("/tool-config/:tool_name", handler.UpdateToolConfig) // 开关某个工具

			// 租户状态管理（启/停租户，仅管理员）
			admin.PUT("/tenant/:id/status", handler.UpdateTenantStatus) // 更新租户状态（0 禁用 / 1 启用）

			// 用量统计查询（租户管理员看本租户的 token 消耗，体现计费能力）
			admin.GET("/usage/today", handler.GetUsageToday)     // 当天用量（token + 调用次数）
			admin.GET("/usage/history", handler.GetUsageHistory) // 最近 N 天用量趋势（?days=N，默认 7，上限 30）

			// 任务列表查询（租户管理员看本租户的异步处理任务，如文档解析进度）
			admin.GET("/task/list", handler.ListTasks) // 分页任务列表（?page&page_size，按 id 倒序）

			// 用户管理：管理员为当前租户创建普通员工（member）
			admin.POST("/user", handler.AdminCreateUser) // 创建员工（tenant_id 从 JWT 取，固定 member）
		}
	}

	return r
}

// AppVersion 服务版本号
const AppVersion = "v0.0.1"
