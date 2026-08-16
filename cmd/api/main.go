package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-platform/agent/engine"
	"agent-platform/agent/interfaces"
	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
	"agent-platform/api"
	"agent-platform/api/handler"
	"agent-platform/api/service"
	"agent-platform/config"
	"agent-platform/llmclient"
	"agent-platform/mq"
	"agent-platform/observability"
	"agent-platform/storage"
	"agent-platform/storage/model"
	"agent-platform/toolkit"

	"go.uber.org/zap"
)

// chatMsgSink 把 storage.AppendChatMessage 适配成 engine.FullHistorySink。
// SessionID 是字符串（对应 sessions.id 的十进制），转成 uint64 落库（纯数字，转换必然成功）。
// TraceID 一律从标准 ctx 取（全链路经 AgentContext.ToContext 已种入，冷轨落库日志带同链路 ID）。
type chatMsgSink struct{}

func (chatMsgSink) Append(ctx context.Context, m engine.ChatMsg) error {
	sid, _ := strconv.ParseUint(m.SessionID, 10, 64)
	return storage.AppendChatMessage(ctx, &model.ChatMessage{
		TenantID:  m.TenantID,
		UserID:    m.UserID,
		SessionID: sid,
		Role:      m.Role,
		Kind:      m.Kind,
		Content:   m.Content,
		TraceID:   interfaces.TraceIDFromCtx(ctx),
	})
}

func main() {
	// 1. 加载配置
	if err := config.Load(); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	cfg := config.GlobalConfig
	log.Println("✅ 配置加载完成")

	// 1.5 初始化结构化 JSON 日志（读 LOG_LEVEL / LOG_FILE），退出前刷新缓冲。
	//     ⚠️ 必须在中间件/各层用 observability.WithContext 之前初始化，
	//     否则全局 logger 为 nop，所有请求级结构化日志（错误/trace_id 关联）会被静默丢弃。
	obsFlush := observability.Init()
	defer obsFlush()
	observability.Info("配置加载完成",
		zap.String("log_level", cfg.Log.Level),
		zap.String("log_file", cfg.Log.File))

	// 2. 初始化 MySQL 连接（从 config 读取配置）
	if err := storage.InitMySQL(); err != nil {
		log.Fatalf("初始化 MySQL 失败: %v", err)
	}
	log.Println("✅ MySQL 连接成功")

	// 3. 初始化 Redis 连接（含连通性 Ping 校验；会话记忆/限流等后续复用）
	if _, err := storage.InitRedis(
		cfg.Redis.Host,
		cfg.Redis.Port,
		cfg.Redis.Password,
		cfg.Redis.DB,
	); err != nil {
		log.Fatalf("初始化 Redis 失败: %v", err)
	}
	log.Printf("✅ Redis 连接成功 (db=%d)", cfg.Redis.DB)

	// 4. 初始化 MinIO 连接（对象存储）
	if err := storage.InitMinIO(); err != nil {
		log.Fatalf("初始化 MinIO 失败: %v", err)
	}
	log.Println("✅ MinIO 连接成功")

	// 5. 初始化 Qdrant 连接（向量数据库，RAG 检索）
	if err := storage.InitQdrant(storage.DefaultVectorSize); err != nil {
		log.Fatalf("初始化 Qdrant 失败: %v", err)
	}
	log.Println("✅ Qdrant 连接成功")

	// 5.5 初始化 RabbitMQ 连接（消息队列：文档解析异步任务后台处理）
	if err := mq.InitRabbitMQ(); err != nil {
		log.Fatalf("初始化 RabbitMQ 失败: %v", err)
	}
	log.Printf("✅ RabbitMQ 连接成功 (vhost=%s, queue=%s)",
		cfg.RabbitMQ.Vhost, cfg.RabbitMQ.QueueName)

	// 6. 初始化工具管理器：注册知识库检索工具 + 文档级检索工具（列表/名称搜索/读全文）+ 注入基于 DB 的工具权限校验
	tm := toolmanager.NewToolManager()
	if err := tm.RegisterTool(toolkit.NewKnowledgeRetrieveTool()); err != nil {
		log.Fatalf("注册知识库检索工具失败: %v", err)
	}
	// 文档维度检索工具（需求单0003）：让 LLM 先知道有哪些文档、按名称拿 ID，再限定文档检索 / 读全文。
	// 三个工具都经 ToolManager 统一执行，天然过 DBPermissionChecker（查不到默认放行，老租户不误拦截）。
	if err := tm.RegisterTool(toolkit.NewListDocumentsTool()); err != nil {
		log.Fatalf("注册文档列表工具失败: %v", err)
	}
	if err := tm.RegisterTool(toolkit.NewSearchDocumentsTool()); err != nil {
		log.Fatalf("注册文档名称搜索工具失败: %v", err)
	}
	if err := tm.RegisterTool(toolkit.NewGetDocumentContentTool()); err != nil {
		log.Fatalf("注册文档全文读取工具失败: %v", err)
	}
	// 注入 tenant_tool_config 白名单权限校验（未显式开启的工具会被拦截）
	tm.SetPermissionChecker(service.NewDBPermissionChecker())
	// 注入工具管理器到 handler（admin 工具开关接口遍历已注册工具及其描述用）
	handler.SetToolManager(tm)
	{
		names := make([]string, 0, len(tm.ListTools()))
		for _, t := range tm.ListTools() {
			names = append(names, t.Name())
		}
		log.Printf("✅ 工具管理器就绪，已注册 %d 个工具: [%s]", len(names), strings.Join(names, ", "))
	}

	// 7. 组装 ReAct 引擎：LLM 客户端(适配) → 引擎(绑定已注册工具/工具权限校验/内置记忆)
	{
		llmCli := llmclient.NewClient(cfg.LLM) // 真实 LLM 客户端（DeepSeek/硅基流动）

		// 注入用量统计钩子：每次 LLM 调用完成后累加 Redis 用量（租户/用户维度，按天）。
		// 引擎在调用 LLM 时已把 tenant_id/user_id 放进 ctx（见 engine.Run 的 WithTenantUser），
		// 用量上报实现从 ctx 提取后累加 —— 业务调用链零侵入。
		if oc, ok := llmCli.(*llmclient.OpenAIClient); ok {
			oc.SetUsageReporter(service.NewUsageReporter())
		}

		llmAdapter := engine.NewLLMAdapter(llmCli) // 适配成 engine.LLMClient 最小接口

		// 底层：Redis 会话记忆（持久化，重启不丢）
		baseMem := memory.NewRedisMemory(storage.RDB)
		agentEngine := engine.NewReActEngine(llmAdapter, tm, baseMem, "")

		// 叠加"超长自动压缩"：用 auto 压缩记忆替换引擎记忆——业务层无感知，
		// 在 AddMessage 时若历史 token 超阈值，Memory 内部自动生成摘要并压缩（引擎只是正常拿历史/加消息）。
		// agentEngine 实现了 memory.Summarizer，故直接作为摘要生成器注入。
		agentEngine.Memory = memory.NewCompressingMemory(baseMem, agentEngine)

		// 注入冷轨完整历史落库 sink：把 storage.AppendChatMessage 接进 FullHistorySink。
		// 对话完整历史（含工具调用全过程）异步落 MySQL chat_messages，永不压缩，供回看/审计。
		// 热轨仍是 Redis 会话记忆（可压缩），冷轨是 MySQL 逐字原文（永不压缩）——双轨分离。
		agentEngine.SetFullHistorySink(chatMsgSink{})

		handler.SetAgentEngine(agentEngine) // 注入 HTTP 对话接口使用
		log.Println("✅ ReAct 引擎就绪，已装配 LLM/工具/Redis记忆(超长自动压缩)/权限校验/冷轨完整历史")
	}

	// 8. 初始化 Gin 引擎（含全局中间件与路由）
	router := api.NewRouter()

	// 9. 启动独立 Prometheus 指标服务（单独端口，内网访问，与公网业务端口隔离）：
	//    - 业务接口是公网暴露的，metrics 不应暴露给公网；
	//    - 单独起一个监听端口（config.MetricsPort，默认 9090，0=禁用），只在内网可达。
	//    这样 /metrics 不经过 Gin 的业务中间件（登录/限流等），直接暴露指标文本。
	if cfg.Server.MetricsPort > 0 {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", observability.MetricsHandler())
		metricSrv := &http.Server{
			Addr:              fmt.Sprintf(":%d", cfg.Server.MetricsPort),
			Handler:           metricsMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Printf("📊 Prometheus 指标服务启动，监听 %s/metrics", metricSrv.Addr)
			if err := metricSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("指标服务异常: %v", err)
			}
		}()
	}

	// 10. 启动 HTTP 服务，监听配置里的端口
	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	log.Printf("🚀 服务启动，监听 %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
