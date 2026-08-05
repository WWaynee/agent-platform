package main

import (
	"fmt"

	"agent-platform/config"
)

func main() {
	if err := config.Load(); err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		return
	}

	cfg := config.GlobalConfig
	fmt.Println("===== 配置加载结果 =====")
	fmt.Printf("MySQL   : %s@%s:%d/%s\n", cfg.MySQL.User, cfg.MySQL.Host, cfg.MySQL.Port, cfg.MySQL.DBName)
	fmt.Printf("Redis   : %s:%d (password:%s)\n", cfg.Redis.Host, cfg.Redis.Port, cfg.Redis.Password)
	fmt.Printf("MinIO   : %s (bucket:%s)\n", cfg.MinIO.Endpoint, cfg.MinIO.Bucket)
	fmt.Printf("Qdrant  : %s:%d\n", cfg.Qdrant.Host, cfg.Qdrant.Port)
	fmt.Printf("JWT     : secret=%s expire=%d 秒 (~%.1fh)\n", cfg.JWT.Secret, cfg.JWT.ExpireSeconds, float64(cfg.JWT.ExpireSeconds)/3600)
	fmt.Printf("LLM     : model=%s base=%s timeout=%ds retry=%d apiKey=%s\n", cfg.LLM.Model, cfg.LLM.BaseURL, cfg.LLM.Timeout, cfg.LLM.MaxRetry, cfg.LLM.APIKey)
	fmt.Printf("Server  : :%d\n", cfg.Server.HTTPPort)
	fmt.Println("=======================")
}
