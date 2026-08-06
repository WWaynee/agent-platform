package main

import (
	"context"
	"fmt"
	"os"

	"agent-platform/config"
	"agent-platform/llmclient"
)

// llm-demo 是 llmclient 的自测入口（周四交付物之一）。
// 用法：
//   go run ./cmd/llm-demo/ chat      → 测试 Chat 对话
//   go run ./cmd/llm-demo/ embed     → 测试 Embedding 向量生成
func main() {
	if err := config.Load(); err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}

	client := llmclient.NewClient(config.GlobalConfig.LLM)
	ctx := context.Background()

	// 默认跑 chat
	action := "chat"
	if len(os.Args) > 1 {
		action = os.Args[1]
	}

	switch action {
	case "chat":
		runChat(ctx, client)
	case "embed":
		runEmbed(ctx, client)
	default:
		fmt.Printf("未知命令 %q，可用: chat / embed\n", action)
	}
}

func runChat(ctx context.Context, client llmclient.Client) {
	req := llmclient.ChatRequest{
		Messages: []llmclient.ChatMessage{
			{Role: llmclient.RoleSystem, Content: "你是一个乐于助人的助手。"},
			{Role: llmclient.RoleUser, Content: "你好"},
		},
		Temperature: 0.7,
	}
	resp, err := client.Chat(ctx, req)
	if err != nil {
		fmt.Printf("Chat 失败: %v\n", err)
		return
	}
	fmt.Printf("回复: %s\n", resp.Content)
	fmt.Printf("token用量: prompt=%d completion=%d total=%d\n",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
}

func runEmbed(ctx context.Context, client llmclient.Client) {
	resp, err := client.Embed(ctx, llmclient.EmbeddingRequest{Input: "多租户智能平台"})
	if err != nil {
		fmt.Printf("Embedding 失败: %v\n", err)
		return
	}
	fmt.Printf("向量维度: %d\n", len(resp.Vector))
	fmt.Printf("向量前5个值: %v\n", resp.Vector[:5])
	fmt.Printf("token用量: prompt=%d total=%d\n", resp.Usage.PromptTokens, resp.Usage.TotalTokens)
}
