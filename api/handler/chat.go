package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"agent-platform/agent/engine"
	"agent-platform/agent/interfaces"
	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
	"agent-platform/api/validator"
)

// ============ 对话接口 ============
//
// POST /api/chat：把 ReAct 引擎暴露成 HTTP 接口。
//   - 必须登录（私有路由组，JWT 鉴权）。
//   - tenant_id / user_id 一律从 JWT 上下文拿（唯一可信来源），绝不信任前端传的。
//   - session_id 关联数据库会话（model.Session）：空则自动创建新会话；非空则校验
//     属于当前租户且属于当前用户，复用其 Redis 对话历史延续上下文。
//
// ⚠️ 多租户：Agent 内部（engine → 工具 → service.Search → storage）的 tenant_id
//    来自这里构造的 engine.AgentContext.TenantID，隔离底线仍在 storage 层强制过滤。

// agentEngine ReAct 引擎实例。由程序启动时（cmd/api/main.go）注入。
var agentEngine *engine.ReActEngine

// SetAgentEngine 注入 ReAct 引擎实例（启动时调用，只注入一次）。
func SetAgentEngine(e *engine.ReActEngine) {
	agentEngine = e
}

// ChatRequest 对话请求
type ChatRequest struct {
	SessionID string `json:"session_id"`                              // 会话 ID（数据库会话主键的字符串形式；空则自动创建新会话）
	Query     string `json:"query" binding:"required,min=1,max=2000"` // 用户提问（必填，长度 1~2000）
	Stream    bool   `json:"stream"`                                  // 是否流式输出（SSE）；true=全流程流式，false=一次性返回
}

// defaultSessionTitle 新会话的默认标题：取首句话前 N 个字，便于列表辨识
func defaultSessionTitle(query string) string {
	runes := []rune(strings.TrimSpace(query))
	if len(runes) > 20 {
		runes = runes[:20]
	}
	return string(runes)
}

// Chat 对话接口
// POST /api/chat
//
// 请求体：{"session_id": "5", "query": "1+1等于几?"}
// 返回：{"answer": "...", "session_id": "5", "tool_calls": [...]}
//
// session_id 处理：
//   - 空 → 自动创建一条数据库新会话，返回其 ID 作为本次 session_id
//   - 非空 → 校验该会话存在且属于当前租户、当前用户；通过则复用其 Redis 历史（多轮）
func Chat(c *gin.Context) {
	// 1. 引擎必须已完成注入
	if agentEngine == nil {
		response.ServerError(c, "Agent 引擎未初始化")
		return
	}

	// 2. 从 JWT 上下文拿租户与用户（唯一可信来源）
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	// 3. 解析请求体（标签校验：query 必填）
	var req ChatRequest
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	// 4. 处理 session_id：空则自动创建新会话，非空则校验归属
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		// 4.1 未传 session_id → 自动创建一条新会话（标题取首个问题）
		//    透传请求级 ctx，使会话建库日志与本次请求共享同一 trace_id。
		id, err := service.CreateSession(c.Request.Context(), tenantID, userID, defaultSessionTitle(req.Query))
		if err != nil {
			response.ServerError(c, "创建会话失败: "+err.Error())
			return
		}
		sessionID = strconv.FormatUint(id, 10)
	} else {
		// 4.2 传了 session_id → 必须校验属于当前租户，且只能复用自己的会话
		id, err := strconv.ParseUint(sessionID, 10, 64)
		if err != nil {
			response.BadRequest(c, "无效的会话 ID")
			return
		}
		s, err := service.GetSessionDetail(c.Request.Context(), tenantID, id)
		if err != nil {
			// 会话不存在或属于别的租户：统一返回"不存在/无权访问"，不区分，防横向探测
			response.Forbidden(c, "会话不存在或无权访问")
			return
		}
		if s.UserID != userID {
			// 别人的会话：同样返回无权限，防止越权读取他人历史
			response.Forbidden(c, "会话不存在或无权访问")
			return
		}
	}

	// 5. 构造 Agent 上下文并运行 ReAct 引擎（历史从 Redis 按 tenant+session 读）
	//    把请求级 trace_id 注入 AgentContext，经 ToContext/WithAgentContext 带进
	//    Agent 全链路（ReAct 迭代 / LLM / 工具）日志，使入口→LLM 共享同一 trace_id。
	actx := engine.AgentContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
	}
	if tid := interfaces.TraceIDFromCtx(c.Request.Context()); tid != "" {
		actx = *actx.WithTraceID(tid)
	}

	// 6. 根据是否流式，走两条分支
	if req.Stream {
		streamChat(c, actx, req.Query, sessionID)
		return
	}

	resp, err := agentEngine.Run(actx, req.Query)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	// 7. 返回回答 + 过程辅助信息
	toolNames := make([]string, 0, len(resp.ToolCalls))
	for _, tc := range resp.ToolCalls {
		toolNames = append(toolNames, tc.ToolName)
	}

	// 审计：记录一次 RAG 问答（尽力而为，不影响主流程）。
	// ctx 已由 JWT 中间件种入 tenant_id/user_id/trace_id，RecordAuditLog 会一并落库。
	// 内容记录会话 ID、问题与命中的工具，便于回溯"谁在哪个会话问了什么、走了什么工具"。
	handleMsg := req.Query
	service.RecordAuditLog(c.Request.Context(), "RAG问答",
		fmt.Sprintf("会话 %s 提问 %q（命中工具: %v）", sessionID, handleMsg, toolNames))

	response.Success(c, gin.H{
		"answer":     resp.Answer,
		"session_id": sessionID,
		"tool_calls": toolNames,
	})
}

// sseWriter 把引擎 ProcessEvent 转为 SSE 事件写往 c.Writer（需求单 0009 全流程流式）。
// 事件格式：event:<type>\ndata:<json>\n\n
type sseWriter struct {
	c   *gin.Context
	fl  http.Flusher
	err error
}

// enable 设置 SSE 响应头，返回 Flusher（不支持的响应则返回 false，调用方应降级为一次性返回）。
func (w *sseWriter) enable() bool {
	w.c.Header("Content-Type", "text/event-stream")
	w.c.Header("Cache-Control", "no-cache")
	w.c.Header("Connection", "keep-alive")
	fl, ok := w.c.Writer.(http.Flusher)
	w.fl = fl
	return ok
}

// write 写一条 SSE 事件并 flush。
func (w *sseWriter) write(evType string, payload []byte) {
	if w.err != nil {
		return
	}
	_, w.err = w.c.Writer.Write([]byte("event: " + evType + "\n"))
	if w.err != nil {
		return
	}
	_, w.err = w.c.Writer.Write([]byte("data: " + string(payload) + "\n\n"))
	if w.err == nil && w.fl != nil {
		w.fl.Flush()
	}
}

// writeJSON 写一条 data 为 JSON 的 SSE 事件。
func (w *sseWriter) writeJSON(evType string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	w.write(evType, b)
}

// progressToSSE 把引擎 ProcessEvent 转成 SSE 写出的回调。
// 逐字输出：收到 answer_text 事件时，把完整回答按字（rune）切分，逐字发 answer_token；
// 结束时发 done（含完整 answer / tool_calls / session_id）。
func (w *sseWriter) progressToSSE(sessionID string) engine.ProgressFunc {
	return func(ev engine.ProgressEvent) {
		switch ev.Type {
		case engine.ProgressThinking:
			w.writeJSON("thinking", gin.H{"message": ev.Message})
		case engine.ProgressToolCall:
			w.writeJSON("tool_call", gin.H{"tool": ev.ToolName, "message": ev.Message})
		case engine.ProgressToolResult:
			w.writeJSON("tool_result", gin.H{"tool": ev.ToolName, "result": ev.Result})
		case engine.ProgressAnswerText:
			// 逐字输出打字机：把整段回答按 rune 逐个发 answer_token
			runes := []rune(ev.Text)
			for _, r := range runes {
				w.writeJSON("answer_token", gin.H{"delta": string(r)})
			}
		case engine.ProgressDone:
			w.writeJSON("done", gin.H{
				"answer":     ev.Answer,
				"session_id": sessionID,
				"tool_calls": ev.ToolCalls,
			})
		}
	}
}

// streamChat 以 SSE 全流程流式处理一次对话（需求单 0009）。
// 说明：引擎在 Run 内通过 Progress 回调逐步上报"思考/工具/回答"，这里转成 SSE 写给前端。
// 冷轨完整历史（persistFullHistory）仍在引擎 Run 内部落库，不受此影响。
func streamChat(c *gin.Context, actx engine.AgentContext, query, sessionID string) {
	sw := &sseWriter{c: c}
	if !sw.enable() {
		// 响应不支持流式（异常），退回一次性返回兜底
		resp, err := agentEngine.Run(actx, query)
		if err != nil {
			response.ServerError(c, err.Error())
			return
		}
		response.Success(c, gin.H{"answer": resp.Answer, "session_id": sessionID, "tool_calls": resp.ToolCalls})
		return
	}

	// 全流程流式：以「单次调用参数」方式传进度回调，避免全局 SetProgress 被并发请求污染。
	_, err := agentEngine.RunWithProgress(actx, query, sw.progressToSSE(sessionID))

	if err != nil {
		// Run 返回错误（正常情况下引擎内部已降级为兜底 answer；这里兜底补一条 done 保证前端能收尾）
		sw.writeJSON("done", gin.H{"answer": "", "session_id": sessionID, "tool_calls": nil, "error": err.Error()})
		return
	}
	// 正常情况下 done 已由引擎的 ProgressDone 发出；Run 返回后再补 flush 确保完整
	if sw.err == nil && sw.fl != nil {
		sw.fl.Flush()
	}
}
