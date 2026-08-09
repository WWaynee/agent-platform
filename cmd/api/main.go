package main

import (
	"fmt"
	"log"
	"strings"

	"agent-platform/agent/engine"
	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
	"agent-platform/api"
	"agent-platform/api/handler"
	"agent-platform/api/service"
	"agent-platform/config"
	"agent-platform/llmclient"
	"agent-platform/storage"
	"agent-platform/toolkit"
)

func main() {
	// 1. 加载配置
	if err := config.Load(); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	cfg := config.GlobalConfig
	log.Println("✅ 配置加载完成")

	// 2. 初始化 MySQL 连接（从 config 读取配置）
	if err := storage.InitMySQL(); err != nil {
		log.Fatalf("初始化 MySQL 失败: %v", err)
	}
	log.Println("✅ MySQL 连接成功")

	// 3. 初始化 Redis 连接
	if _, err := storage.InitRedis(
		cfg.Redis.Host,
		cfg.Redis.Port,
		cfg.Redis.Password,
	); err != nil {
		log.Fatalf("初始化 Redis 失败: %v", err)
	}
	log.Println("✅ Redis 连接成功")

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

	// 6. 初始化工具管理器：注册知识库检索工具 + 注入基于 DB 的工具权限校验
	tm := toolmanager.NewToolManager()
	if err := tm.RegisterTool(toolkit.NewKnowledgeRetrieveTool()); err != nil {
		log.Fatalf("注册知识库检索工具失败: %v", err)
	}
	// 注入 tenant_tool_config 白名单权限校验（未显式开启的工具会被拦截）
	tm.SetPermissionChecker(service.NewDBPermissionChecker())
	{
		names := make([]string, 0, len(tm.ListTools()))
		for _, t := range tm.ListTools() {
			names = append(names, t.Name())
		}
		log.Printf("✅ 工具管理器就绪，已注册 %d 个工具: [%s]", len(names), strings.Join(names, ", "))
	}

	// 7. 组装 ReAct 引擎：LLM 客户端(适配) → 引擎(绑定已注册工具/工具权限校验/内置记忆)
	{
		llmCli := llmclient.NewClient(cfg.LLM)     // 真实 LLM 客户端（DeepSeek/硅基流动）
		llmAdapter := engine.NewLLMAdapter(llmCli) // 适配成 engine.LLMClient 最小接口
		mem := memory.NewInMemoryMemory()          // 会话记忆（当前内存版）
		agentEngine := engine.NewReActEngine(llmAdapter, tm, mem, "")
		handler.SetAgentEngine(agentEngine) // 注入 HTTP 对话接口使用
		log.Println("✅ ReAct 引擎就绪，已装配 LLM/工具/记忆/权限校验")
	}

	// 8. 初始化 Gin 引擎（含全局中间件与路由）
	router := api.NewRouter()

	// 9. 启动 HTTP 服务，监听配置里的端口
	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	log.Printf("🚀 服务启动，监听 %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
