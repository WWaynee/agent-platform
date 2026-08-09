package engine

import (
	"context"

	"agent-platform/llmclient"
)

// ============ LRM 客户端适配器（把 llmclient.Client 适配成 engine.LLMClient） ============
//
// engine.LLMClient 只依赖"能 Chat 并返回文本"的最小接口（屏蔽厂商细节）。
// llmclient.Client 是面向具体厂商(OpenAI兼容)的完整客户端接口（含 Embed 等 RAG 能力）。
// 本适配器把后者薄薄包一层，转成引擎要的最小接口，解耦：引擎不感知 llmclient / 厂商。

// llmAdapter 实现 engine.LLMClient 接口。
type llmAdapter struct {
	client llmclient.Client // 底层完整客户端
}

// NewLLMAdapter 把 llmclient.Client 适配成 engine.LLMClient。
func NewLLMAdapter(client llmclient.Client) LLMClient {
	if client == nil {
		panic("LLM 适配器构造失败：底层 llmclient 不能为 nil")
	}
	return &llmAdapter{client: client}
}

// Chat 实现 engine.LLMClient：把 engine 侧消息转成 llmclient 侧结构，调用后返回回复文本。
func (a *llmAdapter) Chat(ctx context.Context, req ChatRequest) (string, error) {
	// 引擎侧 Message → llmclient 侧 ChatMessage
	msgs := make([]llmclient.ChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, llmclient.ChatMessage{Role: m.Role, Content: m.Content})
	}

	resp, err := a.client.Chat(ctx, llmclient.ChatRequest{
		Messages:    msgs,
		Temperature: 0, // 工具决定/事实类问题用低温度，更稳定
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// compile-time 断言：*llmAdapter 实现 engine.LLMClient。
var _ LLMClient = (*llmAdapter)(nil)
