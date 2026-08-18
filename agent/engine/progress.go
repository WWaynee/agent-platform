package engine

// ============ 过程进度事件（全流程流式，需求单 0009） ============
//
// 引擎在 ReAct 各阶段通过 ProgressFunc 回调把"进行到哪一步"逐步推给外部
// （通常由 HTTP handler 接成 SSE 发给前端），实现"思考中 / 工具调用 / 逐字回答"的实时反馈。
//
// 事件时序（一次回答）：
//   thinking     ：引擎要调 LLM（每次 ReAct 迭代 / 思考），前端展示"正在思考…"
//   tool_call    ：要调用某工具，前端展示"正在调用 xxx 工具…"
//   tool_result  ：工具已返回（带 result），前端可展示工具返回（可用 tool_call 后接 result 展示）
//   answer_text  ：携带最终回答的**一段文本**（用于逐字输出：外部可把整段切字逐字渲染，或累积）
//   done         ：本次回答结束，携带完整最终回答 + 工具调用清单，供外部收尾（冷轨落库等）
//
// 说明：answer_text 事件携带的是最终回答整段（非逐 token 的 llm 原生流式分段）——
// 为避免依赖 DeepSeek 等厂商的 SSE token 流式（兼容/超时/熔断风险大），当前实现采用
// "引擎一次性拿到最终回答 → 经 answer_text 推给外部 → 外部(前端/handler)按需逐字打字机渲染"。
// 这已满足"最终回答逐字输出 + 阶段状态实时反馈"，且稳定、向后兼容。
// 若后续要厂商级 token 流式，可在 LLMClient 层扩展 ChatStream 并在此累积增量，协议不变。

// ProgressEventType 过程事件类型。
type ProgressEventType string

const (
	// ProgressThinking 引擎要调 LLM 开始思考。
	ProgressThinking ProgressEventType = "thinking"
	// ProgressToolCall 引擎要调用某个工具。
	ProgressToolCall ProgressEventType = "tool_call"
	// ProgressToolResult 工具已返回结果。
	ProgressToolResult ProgressEventType = "tool_result"
	// ProgressAnswerText 最终回答文本就绪（携带一段文本，可逐字渲染）。
	ProgressAnswerText ProgressEventType = "answer_text"
	// ProgressDone 本次回答结束（携带完整最终回答 + 工具调用清单）。
	ProgressDone ProgressEventType = "done"
)

// ProgressEvent 一条过程事件。
type ProgressEvent struct {
	Type     ProgressEventType `json:"type"`
	Message  string            `json:"message,omitempty"`    // 供前端展示的文案
	ToolName string            `json:"tool_name,omitempty"`  // tool_call/tool_result 用
	Result   string            `json:"result,omitempty"`     // tool_result 带工具返回原文
	Text     string            `json:"text,omitempty"`       // answer_text / done 携带的文本
	Answer   string            `json:"answer,omitempty"`     // done 携带完整最终回答
	ToolCalls []string         `json:"tool_calls,omitempty"` // done 携带工具调用清单
	SessionID string           `json:"session_id,omitempty"` // done 携带会话 ID
}

// ProgressFunc 引擎进度回调；nil 安全（外部未注入则跳过）。
type ProgressFunc func(ProgressEvent)

// 说明：进度回调通过 RunWithProgress 以【单次调用参数】方式传入，
// 不使用引擎字段（避免包级单例被并发请求互相覆盖，造成事件串流/死连接，见需求单 0009 修复）。
