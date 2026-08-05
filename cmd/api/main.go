package main

import (
	"fmt"
	"log"

	"agent-platform/api"
	"agent-platform/config"
	"agent-platform/storage"
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

	// 4. 初始化 Gin 引擎（含全局中间件与路由）
	router := api.NewRouter()

	// 5. 启动 HTTP 服务，监听配置里的端口
	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	log.Printf("🚀 服务启动，监听 %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
