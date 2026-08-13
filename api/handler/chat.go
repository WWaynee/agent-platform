package handler

import (
	"fmt"
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
	resp, err := agentEngine.Run(actx, req.Query)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	// 6. 返回回答 + 过程辅助信息
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
