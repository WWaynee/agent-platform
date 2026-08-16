package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
	"agent-platform/observability"

	"go.uber.org/zap"
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
	// defaultToolTimeout 单次工具执行的默认超时上限（10s），防止坏工具无限等待/拖死整轮。
	defaultToolTimeout = 10 * time.Second
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
	// ToolTimeout 单次工具执行的超时上限（默认 10s）。
	// 超时会以「带 deadline 的运行时 ctx」注入工具（经 AgentContext.ToContext 下传），
	// 工具引擎据此监听 ctx.Done() 中止耗时操作，避免单个坏工具把整轮回复拖死/无限等待。
	ToolTimeout time.Duration

	// fullHistorySink 冷轨完整历史落库接口（可选）。nil 则整条冷轨链路静默跳过，
	// 不影响热轨对话（见 SetFullHistorySink / persistFullHistory）。
	fullHistorySink FullHistorySink
}

// SetFullHistorySink 注入冷轨完整历史落库 sink。
// nil 安全：传 nil 等价于关闭冷轨（persistFullHistory 内会判空跳过）。
func (e *ReActEngine) SetFullHistorySink(s FullHistorySink) {
	e.fullHistorySink = s
}

// toolRecord 记录一次工具调用的完整过程（指令 + 执行结果），供冷轨落库 + 热轨模板化摘要。
type toolRecord struct {
	call   ToolCall // 工具调用指令（工具名 + 参数）
	result string   // 工具执行结果（观察结果 out）
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
		ToolTimeout:   defaultToolTimeout, // 默认单次工具超时
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
	var toolRecords []toolRecord // 本轮工具调用完整记录（含指令+结果），供冷轨/热轨摘要
	var lastRaw string           // 记录 LLM 最后一次原始输出（兜底用）
	parseRetry := 0              // 解析失败重试次数（最多 1 次）

	// 引擎运行日志统一经 AgentContext 取 logger：自动带 trace_id / tenant_id / user_id，
	// 使 Agent 全链路（本轮迭代/LLM/工具）与 HTTP 入口共享同一 trace_id。
	alogger := observability.WithAgentContext(ctx)

	// 3/4. ReAct 主循环
	for iter := 0; iter < e.MaxIterations; iter++ {
		alogger.Info("ReAct 进入本轮",
			zap.String("session_id", ctx.SessionID),
			zap.Int("iteration", iter+1),
			zap.Int("max_iterations", e.MaxIterations))

		// a. 调 LLM 生成下一步输出（想）
		//    用携带租户/用户/trace_id 标识的 ctx 调用，让下游用量统计/配额/日志能从 ctx 提取归属。
		//    ⚠️ 不复用 context.Background() 直接裸用——改为基于上下文构造，避免丢失 trace_id。
		llmCtx := ctx.ToContext(nil)
		raw, err := e.LLMClient.Chat(llmCtx, ChatRequest{Messages: msgs})
		if err != nil {
			// LLM 调用失败 → 降级：不 panic、不裸抛错误，改返回友好兜底回答，
			// 并把原始错误塞进 meta 供审计（answer 对用户友好，错误详情对开发可见）。
			resp := &AgentResponse{Answer: "抱歉，模型服务暂时不可用，请稍后再试。"}
			resp.ToolCalls = calls
			resp.WithMeta("error", fmt.Errorf("调用 LLM 失败: %w", err))
			return resp, nil
		}
		lastRaw = raw
		alogger.Info("ReAct LLM 输出", zap.String("session_id", ctx.SessionID), zap.Int("iteration", iter+1))

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
			e.persist(ctx, query, parsed.Input, toolRecords)
			return &AgentResponse{Answer: parsed.Input, ToolCalls: calls}, nil
		}

		// f/g. 工具调用：记录审计 + 经 ToolManager 统一执行（含租户权限校验）
		//     统一解析一次参数，同时供 ToolCall 审计与 toolRecord 记录使用。
		callParams := rawInputToParams(parsed.Input)
		calls = append(calls, ToolCall{ToolName: parsed.Action, Params: callParams})

		// 工具执行超时保护：为本次调用注入「带 deadline 的运行时 ctx」。
		// 以携带 tenant/user/trace_id 的基准 ctx 派生，经 AgentContext.WithRuntimeContext
		// 交给工具（工具内每次 ctx.ToContext(nil) 都会拿到带超时的 ctx），
		// 工具据此 select ctx.Done() 中止耗时操作——避免单个坏工具拖死整轮、无限等待。
		toolCtx := ctx.ToContext(nil) // 基准：带上租户/用户/trace_id
		if e.ToolTimeout > 0 {
			cctx, cancel := context.WithTimeout(toolCtx, e.ToolTimeout)
			toolCtx = cctx
			defer cancel()
		}
		out, terr := e.ToolManager.ExecuteTool(*(ctx.WithRuntimeContext(toolCtx)), parsed.Action, parsed.Input)
		if terr != nil {
			out = fmt.Sprintf("工具 %q 执行失败: %v", parsed.Action, terr)
		}
		// 收集工具调用记录（供冷轨落库 + 热轨模板化摘要）
		toolRecords = append(toolRecords, toolRecord{
			call:   ToolCall{ToolName: parsed.Action, Params: callParams},
			result: out,
		})
		alogger.Info("ReAct 工具调用完成",
			zap.String("session_id", ctx.SessionID),
			zap.String("tool_name", parsed.Action),
			zap.Bool("ok", terr == nil))

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
	e.persist(ctx, query, resp.Answer, toolRecords)
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

// persist 把"本轮对话"写回两处：热轨（Memory，可压缩）+ 冷轨（完整原文落库）。
//
// ① 热轨（Redis，超长自动压缩）——只存"用户提问 + 工具调度汇总 + 最终回答"：
//    - 工具调用不再丢，改成一句**模板化摘要**（summarizeToolCall，零额外 LLM 调用）以 user 角色写入，
//      既让下一轮 LLM 记得上轮调过什么工具、拿到什么结论，又不把巨量工具原文塞进上下文；
//    - 中间轮次的 LLM 思考不写（占 token 高且价值低），只留最终问答 + 工具摘要。
//    装配了 CompressingMemory 时，若该会话累计超长会由 Memory 自动触发摘要压缩，引擎无需关心。
// ② 冷轨（MySQL，永不压缩）——完整原文逐条落库（用户提问、工具指令、工具结果、最终回答）：
//    异步后台写，不阻塞对话响应；任一条失败只 warn（对齐 RecordAuditLog 的"尽力而为"风格）。
func (e *ReActEngine) persist(ctx AgentContext, query, answer string, toolRecords []toolRecord) {
	// ① 热轨：写最终问答 + 每轮工具调用的一句话模板化摘要
	e.Memory.AddMessage(ctx.TenantID, ctx.SessionID, memory.ChatMessage{Role: memory.RoleUser, Content: query})
	for _, tr := range toolRecords {
		e.Memory.AddMessage(ctx.TenantID, ctx.SessionID, memory.ChatMessage{
			Role:    memory.RoleUser, // 规避 tool 角色喂 LLM 的 DeepSeek 协议坑，用 user 承载
			Content: summarizeToolCall(tr),
		})
	}
	e.Memory.AddMessage(ctx.TenantID, ctx.SessionID, memory.ChatMessage{Role: memory.RoleAssistant, Content: answer})

	// ② 冷轨：完整原文异步逐条落库（尽力而为，不阻塞对话）
	e.persistFullHistory(ctx, query, answer, toolRecords)
}

// persistFullHistory 冷轨完整历史落库（异步、尽力而为）。
// 用独立的后台 goroutine 写 MySQL，不阻塞对话主流程；失败只 warn（审计风格）。
// 顺序保证：多条 Append 在同一个 goroutine 内按序执行，保留对话真实时序。
func (e *ReActEngine) persistFullHistory(ctx AgentContext, query, answer string, toolRecords []toolRecord) {
	sink := e.fullHistorySink
	if sink == nil {
		return // 未注入冷轨 sink（关闭冷轨），静默跳过
	}

	// 组装本次全部待落库消息（提问、每个工具指令+结果、最终回答），按时间正序。
	msgs := make([]ChatMsg, 0, 2+2*len(toolRecords))
	appendMsg := func(role, kind, content string) {
		msgs = append(msgs, ChatMsg{
			TenantID:  ctx.TenantID,
			UserID:    ctx.UserID,
			SessionID: ctx.SessionID,
			Role:      role,
			Kind:      kind,
			Content:   content,
		})
	}

	appendMsg("user", "question", query)
	for _, tr := range toolRecords {
		// 工具调用指令：Content 存「工具名 + 参数（序列化成的 JSON 字符串）」
		appendMsg("tool", "tool_call", tr.call.ToolName+" "+toolParamsJSON(tr.call.Params))
		// 工具执行结果：Content 存工具返回的完整原文
		appendMsg("tool", "tool_result", tr.result)
	}
	appendMsg("assistant", "answer", answer)

	// 异步后台写入：基于 AgentContext 译出的标准 ctx 派生（保留 trace_id/tenant_id/user_id），
	// 不能复用请求上下文（请求结束会被 cancel）。每条失败只 warn、不阻塞。
	go func() {
		base := ctx.ToContext(nil) // 带 tenant/user/trace_id 的标准 ctx
		alogger := observability.WithAgentContext(ctx)
		for _, m := range msgs {
			if err := sink.Append(base, m); err != nil {
				alogger.Warn("写完整历史失败",
					zap.String("role", m.Role),
					zap.String("kind", m.Kind),
					zap.Error(err))
			}
		}
	}()
}

// toolParamsJSON 把工具调用参数（map）序列化成 JSON 字符串，供冷轨 tool_call 落库。
// 序列化失败时降级为原样 Key-Value 文本，保证不因参数格式问题丢记录。
func toolParamsJSON(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	b, err := json.Marshal(params)
	if err != nil {
		// 降级：逐 key 拼接（尽力而为）
		var kvs []string
		for k, v := range params {
			kvs = append(kvs, k+"="+fmt.Sprintf("%v", v))
		}
		return strings.Join(kvs, ", ")
	}
	return string(b)
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
