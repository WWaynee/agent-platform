package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
	"agent-platform/api/validator"
)

// ============ 会话接口 ============
//
// 会话：一段多轮对话的元信息（标题/归属），会话的对话消息内容存 Redis。
// 三个接口都在私有路由组（挂 JWT 鉴权中间件），必须登录才能调用。
//
// ⚠️ 多租户/多用户：tenant_id / user_id 一律从 JWT 上下文拿（唯一可信来源，不信前端），
//    列表只返回当前用户会话，删除只能删自己的会话。

// CreateSessionRequest 创建会话请求体
type CreateSessionRequest struct {
	Title string `json:"title" binding:"omitempty,max=100"` // 会话标题（可不传，空则用默认标题；最多 100 字）
}

// CreateSession 创建会话
// POST /api/session
// tenant_id / user_id 从 JWT 拿，创建属于当前用户/租户的会话
func CreateSession(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)
	if tenantID == 0 {
		response.Unauthorized(c, "未获取到租户信息")
		return
	}
	if userID == 0 {
		response.Unauthorized(c, "未获取到用户信息")
		return
	}

	var req CreateSessionRequest
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "新会话"
	}

	id, err := service.CreateSession(c.Request.Context(), tenantID, userID, title)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"id":    id,
		"title": title,
	})
}

// ListSessionReq 会话列表请求（query 参数）
type ListSessionReq struct {
	Page     int `form:"page" binding:"omitempty,min=1"`              // 页码，从 1 起
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"` // 每页条数，1~100
}

// GetSessionList 会话列表
// GET /api/session/list
// 只返回当前用户（当前租户）的会话，按更新时间倒序，分页
func GetSessionList(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	var req ListSessionReq
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, "参数错误: "+err.Error())
		return
	}

	list, total, err := service.GetSessionList(c.Request.Context(), tenantID, userID, req.Page, req.PageSize)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"list":      list,
		"total":     total,
		"page":      req.Page,
		"page_size": req.PageSize,
	})
}

// DeleteSession 删除会话
// DELETE /api/session/:id
// 校验存在且属于当前用户 → 软删数据库 → 同步删 Redis 消息历史
func DeleteSession(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的会话 ID")
		return
	}

	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	if err := service.DeleteSession(c.Request.Context(), tenantID, userID, id); err != nil {
		// 会话不存在 / 跨租户 / 无权删除他人会话，统一返回 400
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, nil)
}

// GetSessionMessages 查询某会话的对话历史
// GET /api/session/:id/messages
// 校验该会话属于当前租户且属于当前用户，才从 Redis 读回其消息历史返回。
// 越权访问（他租户/他人会话）统一返回"会话不存在或无权访问"，不泄露任何内容。
func GetSessionMessages(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的会话 ID")
		return
	}

	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	msgs, err := service.GetSessionMessages(c.Request.Context(), tenantID, userID, id)
	if err != nil {
		// 会话不存在 / 跨租户 / 无权访问他人会话：统一回"无权访问"，防横向探测
		response.Forbidden(c, "会话不存在或无权访问")
		return
	}

	response.Success(c, gin.H{
		"session_id": id,
		"messages":   msgs,
	})
}
