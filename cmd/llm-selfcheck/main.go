package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"agent-platform/config"
	"agent-platform/llmclient"
)

// llm-selfcheck 是 llmclient 的自测入口，对应「自测标准」：
//
//	① Chat：调用发 "你好"，能收到回复
//	② Embedding：传一段文本，能返回向量数组
//	③ 能正确打印消耗的 token 数
//
// 用法：
//
//	go run ./cmd/llm-selfcheck/
//
// 全部通过输出 ✅，否则输出 ❌ 并退出码非 0。
func main() {
	if err := config.Load(); err != nil {
		fmt.Printf("❌ 加载配置失败: %v\n", err)
		os.Exit(1)
	}

	client := llmclient.NewClient(config.GlobalConfig.LLM)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	passed := true

	// ---- 自测①：Chat 发 "你好"，能收到回复 ----
	fmt.Println("【自测①】Chat 发送 \"你好\"...")
	chatResp, err := client.Chat(ctx, llmclient.ChatRequest{
		Messages: []llmclient.ChatMessage{
			{Role: llmclient.RoleSystem, Content: "你是一个乐于助人的中文助手。"},
			{Role: llmclient.RoleUser, Content: "你好"},
		},
		Temperature: 0.7,
	})
	if err != nil {
		fmt.Printf("  ❌ Chat 失败: %v\n", err)
		passed = false
	} else {
		fmt.Printf("  ✅ 收到回复: %s\n", chatResp.Content)
		fmt.Printf("  ✅ Token 消耗: prompt=%d completion=%d total=%d\n",
			chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens, chatResp.Usage.TotalTokens)
		if len(chatResp.Content) == 0 {
			fmt.Println("  ❌ 回复内容为空")
			passed = false
		}
		if chatResp.Usage.TotalTokens <= 0 {
			fmt.Println("  ❌ TotalTokens 未正确返回")
			passed = false
		}
	}

	fmt.Println()

	// ---- 自测②：Embedding 传一段文本，返回向量数组 ----
	fmt.Println("【自测②】Embedding 传一段文本...")
	text := "多租户智能 Agent 工作台，企业内部知识库问答系统。"
	embedResp, err := client.Embed(ctx, llmclient.EmbeddingRequest{Input: text})
	if err != nil {
		fmt.Printf("  ❌ Embedding 失败: %v\n", err)
		passed = false
	} else {
		fmt.Printf("  ✅ 返回向量数组，维度: %d\n", len(embedResp.Vector))
		if len(embedResp.Vector) > 0 {
			fmt.Printf("  ✅ 向量前3个值: %v\n", embedResp.Vector[:min(3, len(embedResp.Vector))])
		} else {
			fmt.Println("  ❌ 向量数组为空")
			passed = false
		}
		fmt.Printf("  ✅ Token 消耗: prompt=%d completion=%d total=%d\n",
			embedResp.Usage.PromptTokens, embedResp.Usage.CompletionTokens, embedResp.Usage.TotalTokens)
		if embedResp.Usage.TotalTokens <= 0 {
			fmt.Println("  ❌ Embedding token 未正确返回")
			passed = false
		}
	}

	fmt.Println()

	// ---- 汇总 ----
	if passed {
		fmt.Println("✅ 全部自测通过：Chat 收到回复 / Embedding 返回向量 / Token 数正确打印")
		os.Exit(0)
	} else {
		fmt.Println("❌ 存在未通过项，请检查上方输出")
		os.Exit(1)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
