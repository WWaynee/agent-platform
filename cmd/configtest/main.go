package main

import (
	"fmt"

	"agent-platform/config"
)

// maskKey 给密钥打码，只保留前 4 位，避免完整打印泄露（仅用于本地查看配置是否加载成功）
func maskKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return key[:4] + "****(len:" + fmt.Sprintf("%d", len(key)) + ")"
}

func main() {
	if err := config.Load(); err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		return
	}

	cfg := config.GlobalConfig
	fmt.Println("===== 配置加载结果 =====")
	fmt.Printf("MySQL   : %s@%s:%d/%s\n", cfg.MySQL.User, cfg.MySQL.Host, cfg.MySQL.Port, cfg.MySQL.DBName)
	fmt.Printf("Redis   : %s:%d (password:%s)\n", cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password)
	fmt.Printf("MinIO   : %s (bucket:%s, useSSL:%v)\n", cfg.MinIO.Endpoint, cfg.MinIO.Bucket, cfg.MinIO.UseSSL)
	fmt.Printf("Qdrant  : %s:%d (collection:%s)\n", cfg.Qdrant.Host, cfg.Qdrant.Port, cfg.Qdrant.CollectionName)
	fmt.Printf("JWT     : secret=%s expire=%d 秒 (~%.1fh)\n", maskKey(cfg.JWT.Secret), cfg.JWT.ExpireSeconds, float64(cfg.JWT.ExpireSeconds)/3600)
	fmt.Printf("LLM     : chatModel=%s embedModel=%s\n", cfg.LLM.ChatModel, cfg.LLM.EmbeddingModel)
	fmt.Printf("          base=%s timeout=%ds maxRetries=%d\n", cfg.LLM.BaseURL, cfg.LLM.Timeout, cfg.LLM.MaxRetries)
	fmt.Printf("          apiKey=%s (已打码)\n", maskKey(cfg.LLM.APIKey))
	fmt.Printf("Server  : :%d\n", cfg.Server.HTTPPort)
	fmt.Println("=======================")
}
