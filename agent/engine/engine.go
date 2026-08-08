package engine

import (
	"context"

	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
)

// ============ LLM 客户端接口 ============

// LLMClient 屏蔽具体大模型客户端的差异，引擎只依赖此接口完成对话。
// 具体实现可由 llmclient（DeepSeek/硅基流动）适配而来。
// 作用：引擎"跟大模型对话"，返回模型生成的文本（可能是 Action 或最终回答）。
type LLMClient interface {
	// Chat 向模型发送一串消息，返回模型生成的文本。
	// 该文本可能是继续思考（Thought/Action）也可能是最终回答（Final Answer），
	// 由引擎后续解析判断。
	Chat(ctx context.Context, req ChatRequest) (string, error)
}

// ChatRequest 引擎侧发给 LLM 的对话请求。
// 引擎会在调用前自行拼好 messages（system 提示 + 工具描述 + 历史 + 最新提问）。
type ChatRequest struct {
	// Messages 消息列表（含上下文），Message 定义见下。
	Messages []Message
}

// Message 一条引擎侧的对话消息（对齐 llmclient 的 role/content 模型）。
type Message struct {
	Role    string // system / user / assistant / tool
	Content string
}

// ============ ReAct 引擎主结构体 ============

// ReActEngine 是 ReAct（Reason + Act）调度引擎的主结构体。
// 它本身不实现具体业务，只负责调度：让 LLM 想（Reason）→ 调工具（Act）→
// 再想 → …… 直到拿到最终答案，或达到最大迭代轮次。
//
// 持有三个核心组件：
//   - LLMClient   负责跟大模型对话（"想"）
//   - ToolManager 负责调用工具（"做"）
//   - Memory      负责记历史（上下文来源）
//
// MaxInterations 防止无限循环；SystemPrompt 是每次轮次的基础系统提示。
type ReActEngine struct {
	// LLMClient 持有 LLM 客户端接口，负责与模型对话、生成 Thought/Action。
	LLMClient LLMClient
	// ToolManager 持有工具管理器，负责注册的工具的查找与统一执行。
	ToolManager *toolmanager.ToolManager
	// Memory 持有记忆管理器，负责会话历史的存取。
	Memory memory.Memory

	// MaxIterations 最大迭代轮次，防止 Agent 无限循环（默认 5 次）。
	MaxIterations int
	// SystemPrompt 系统提示词模板，定义 Agent 的角色与行为准则。
	SystemPrompt string
}

// NewReActEngine 构造一个 ReAct 引擎。
// 参数：
//   - llm   ：实现了 LLMClient 接口的对话客户端（必填，nil 会 panic）
//   - tools ：工具管理器（必填）
//   - mem   ：记忆管理器（必填）
//   - systemPrompt：系统提示词模板
//
// MaxIterations 若未传（<=0），使用默认值 5。
func NewReActEngine(llm LLMClient, tools *toolmanager.ToolManager, mem memory.Memory, systemPrompt string) *ReActEngine {
	if llm == nil {
		panic("ReAct 引擎构造失败：LLMClient 不能为 nil")
	}
	if tools == nil {
		panic("ReAct 引擎构造失败：ToolManager 不能为 nil")
	}
	if mem == nil {
		panic("ReAct 引擎构造失败：Memory 不能为 nil")
	}

	return &ReActEngine{
		LLMClient:     llm,
		ToolManager:   tools,
		Memory:        mem,
		MaxIterations: 5, // 默认 5 次迭代上限
		SystemPrompt:  systemPrompt,
	}
}
