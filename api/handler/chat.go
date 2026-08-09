package handler

import (
	"strings"

	"github.com/gin-gonic/gin"

	"agent-platform/agent/engine"
	"agent-platform/api/middleware"
	"agent-platform/api/response"
)

// ============ 对话接口 ============
//
// POST /api/chat：把 ReAct 引擎暴露成 HTTP 接口。
//   - 必须登录（私有路由组，JWT 鉴权）。
//   - tenant_id / user_id 一律从 JWT 上下文拿（唯一可信来源），绝不信任前端传的。
//   - session_id 由前端传入，用于标识一段多轮对话（同名会话延续上下文）。
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
	SessionID string `json:"session_id"` // 会话 ID（可选；空则用"匿名会话"）
	Query     string `json:"query"`      // 用户提问（必填，不能为空）
}

// Chat 对话接口
// POST /api/chat
//
// 请求体：{"session_id": "s1", "query": "1+1等于几?"}
// 返回：{"answer": "...", "tool_calls": [...]}
func Chat(c *gin.Context) {
	// 1. 引擎必须已完成注入
	if agentEngine == nil {
		response.ServerError(c, "Agent 引擎未初始化")
		return
	}

	// 2. 从 JWT 上下文拿租户与用户（唯一可信来源）
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	// 3. 解析请求体
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		response.BadRequest(c, "query 不能为空")
		return
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = "anonymous"
	}

	// 4. 构造 Agent 上下文并运行 ReAct 引擎
	actx := engine.AgentContext{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: sessionID,
	}
	resp, err := agentEngine.Run(actx, req.Query)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	// 5. 返回回答 + 过程辅助信息
	toolNames := make([]string, 0, len(resp.ToolCalls))
	for _, tc := range resp.ToolCalls {
		toolNames = append(toolNames, tc.ToolName)
	}
	response.Success(c, gin.H{
		"answer":     resp.Answer,
		"session_id": sessionID,
		"tool_calls": toolNames,
	})
}
