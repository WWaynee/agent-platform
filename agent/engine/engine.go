package engine

import (
	"context"
	"fmt"
	"log"

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

// 引擎运行相关常量
const (
	// finalAnswerAction 预留的终止动作名：LLM 输出该 action 即表示直接回答，不再调工具。
	finalAnswerAction = "final_answer"
	// maxParseRetries LLM 输出解析失败时的最大重试次数（防止无限重试）。
	maxParseRetries = 1
)

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

// Run 执行一次 ReAct 调度，是引擎对外的主入口。
// 输入一次用户提问(query)与执行上下文(ctx)，返回最终回答与过程元信息。
//
// 核心循环（思考→行动→观察，最多 MaxIterations 轮）：
//  1. 从 Memory 读该会话历史；组装 system(角色+工具列表+格式) + 历史 + 当前问题。
//  2. 调 LLM 得到一段输出 → 解析为 {action, action_input}。
//  3. final_answer → 结束；否则当工具调用执行，把结果(观察)喂回上下文继续下一轮。
//  4. 解析失败 → 塞回纠错提示重试(最多1次)；达最大轮次 → 强制兜底收尾。
//  5. 把本次 user 提问与 assistant 回答写回 Memory。
func (e *ReActEngine) Run(ctx AgentContext, query string) (*AgentResponse, error) {
	// 1. 从 Memory 读取该会话历史（正序）；带 tenantID 实现"按租户+会话"隔离存取
	history := e.Memory.GetHistory(ctx.TenantID, ctx.SessionID)

	// 2. 组装消息序列：system + 历史 + 当前问题
	msgs := e.buildInitialMessages(ctx, history, query)

	var calls []ToolCall
	var lastRaw string // 记录 LLM 最后一次原始输出（兜底用）
	parseRetry := 0    // 解析失败重试次数（最多 1 次）

	// 3/4. ReAct 主循环
	for iter := 0; iter < e.MaxIterations; iter++ {
		log.Printf("[ReAct] 会话=%s 第 %d/%d 轮", ctx.SessionID, iter+1, e.MaxIterations)

		// a. 调 LLM 生成下一步输出（想）
		raw, err := e.LLMClient.Chat(context.Background(), ChatRequest{Messages: msgs})
		if err != nil {
			// LLM 调用失败 → 降级：不 panic、不裸抛错误，改返回友好兜底回答，
			// 并把原始错误塞进 meta 供审计（answer 对用户友好，错误详情对开发可见）。
			resp := &AgentResponse{Answer: "抱歉，模型服务暂时不可用，请稍后再试。"}
			resp.ToolCalls = calls
			resp.WithMeta("error", fmt.Errorf("调用 LLM 失败: %w", err))
			return resp, nil
		}
		lastRaw = raw
		log.Printf("[ReAct] LLM 输出: %.180s", raw)

		// b/c. 解析 LLM 输出（是否 final_answer 还是工具调用）
		parsed, perr := parseLLMOutput(raw)
		if perr != nil {
			// d. 解析失败：给 LLM 塞回纠错提示，本轮重试（最多 1 次）
			if parseRetry < maxParseRetries {
				parseRetry++
				hint := fmt.Sprintf("你刚才的输出无法被系统解析成合法的 {action, action_input} 动作，请严格按格式重新输出单条 JSON，不要夹杂其他文字。解析错误: %v", perr)
				msgs = append(msgs, Message{Role: "system", Content: hint})
				continue
			}
			// 重试用尽：强制结束，走兜底
			break
		}

		// e. final_answer → 结束循环，返回答案
		if parsed.Action == finalAnswerAction {
			e.persist(ctx, query, parsed.Input)
			return &AgentResponse{Answer: parsed.Input, ToolCalls: calls}, nil
		}

		// f/g. 工具调用：记录审计 + 经 ToolManager 统一执行（含租户权限校验）
		calls = append(calls, ToolCall{ToolName: parsed.Action, Params: rawInputToParams(parsed.Input)})
		out, terr := e.ToolManager.ExecuteTool(ctx, parsed.Action, parsed.Input)
		if terr != nil {
			out = fmt.Sprintf("工具 %q 执行失败: %v", parsed.Action, terr)
		}
		log.Printf("[ReAct] 调用工具 %s 完成", parsed.Action)

		// h. 把"这次的决策 + 观察结果"追加进上下文，供下一轮继续思考
		// 注意：观察结果用 user 角色而非 tool 角色喂回——本引擎是"文本JSON" ReAct 约定，
		// 并非 OpenAI 原生 function-calling(tool_calls) 协议。若标记为 tool 角色，
		// DeepSeek 等兼容 API 会强制要求前驱 assistant 消息带 tool_calls/tool_call_id 而报 400。
		// 用 user 角色承载观察结果即可稳定兼容，又不失可读性。
		msgs = append(msgs,
			Message{Role: "assistant", Content: raw},
			Message{Role: "user", Content: fmt.Sprintf("工具 %q 的观察结果：\n%s", parsed.Action, out)},
		)
	}

	// 5. 达到最大迭代：强制结束，返回最后一次内容（兜底收尾）
	resp := e.fallbackResponse(lastRaw)
	resp.ToolCalls = calls // 兜底响应也需要附带本轮工具调用记录，避免丢失审计信息
	e.persist(ctx, query, resp.Answer)
	return resp, nil
}

// buildInitialMessages 组装首轮消息序列：system(角色+工具列表+格式) + 历史 + 当前提问。
func (e *ReActEngine) buildInitialMessages(ctx AgentContext, history []memory.ChatMessage, query string) []Message {
	tools := e.ToolManager.ListTools() // 实时取工具列表，保证 Prompt 描述与实际可用一致
	msgs := []Message{systemMessageFor(e.SystemPrompt, tools)}

	for _, h := range history {
		msgs = append(msgs, Message{Role: string(h.Role), Content: h.Content})
	}
	msgs = append(msgs, Message{Role: "user", Content: query})
	return msgs
}

// persist 把"本轮的用户提问 + 引擎最终回答"写回 Memory（user + assistant 两条）。
//
// 设计（中间过程存不存历史）：只存**最终问答**，不存中间过程。
//   - 中间轮次的 LLM 思考（reasoning）与工具观察结果，只在当次请求内部的 msgs 快照里即时消费，
//     事后不写回 Memory——下一轮上下文只需知道"上轮问了什么、答了什么"，无需重放工具调用全过程。
//   - 存得少则占 token 少，Memory 层（CompressingMemory）超长自动压缩也更省。
//   - 工具调用过程不丢：通过 AgentResponse.ToolCalls 返回，供审计 / 前端逐步展示。
//
// 每次写入都经 Memory.AddMessage；因装配了 CompressingMemory，若该会话累计超长，
// 会在写入后由 Memory 自动触发摘要压缩，引擎无需关心压缩细节。
func (e *ReActEngine) persist(ctx AgentContext, query, answer string) {
	e.Memory.AddMessage(ctx.TenantID, ctx.SessionID, memory.ChatMessage{Role: memory.RoleUser, Content: query})
	e.Memory.AddMessage(ctx.TenantID, ctx.SessionID, memory.ChatMessage{Role: memory.RoleAssistant, Content: answer})
}

// fallbackResponse 最大迭代终止时的兜底响应：能解析出 final_answer 就用它，
// 否则返回最后一次 LLM 原始输出，并提示可能未收敛。
func (e *ReActEngine) fallbackResponse(lastRaw string) *AgentResponse {
	if ps, err := parseLLMOutput(lastRaw); err == nil && ps.Action == finalAnswerAction {
		return &AgentResponse{Answer: ps.Input}
	}
	return &AgentResponse{
		Answer: fmt.Sprintf("我已尝试最多 %d 轮仍未收敛到确定答案，请换个问法或补充信息。最后一步模型输出：%s", e.MaxIterations, lastRaw),
	}
}
