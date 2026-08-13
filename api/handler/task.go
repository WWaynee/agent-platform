package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
)

// parsePageGin 从 query string 解析分页参数，带默认值与兜底，非法则回落默认。
// 返回 page / pageSize。页面变量前缀空即返回默认：page=1, pageSize=10。
func parsePageGin(c *gin.Context) (int, int) {
	page := parseIntGin(c.Query("page"), 1)
	pageSize := parseIntGin(c.Query("page_size"), 10)
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}
	return page, pageSize
}

// parseIntGin 把字符串解析为 int；空串或非法返回 fallback。
func parseIntGin(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// ============ Handler：异步任务状态查询 ============

// GetTask 查询异步任务状态详情
// 路径参数：id（任务 ID）
// tenant_id 从 JWT 上下文拿，强制过滤；跨租户/不存在统一返回"任务不存在"。
//
// 用途：前端上传文档拿到 task_id 后，轮询此接口看处理进度
// （pending → processing → success / failed），处理完再刷新文档列表。
func GetTask(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的任务 ID")
		return
	}

	tenantID := middleware.GetTenantID(c)
	task, err := service.GetTaskDetail(c.Request.Context(), tenantID, id)
	if err != nil {
		// 任务不存在/跨租户访问，统一返回 400（带出错提示）
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"id":          task.ID,
		"tenant_id":   task.TenantID,
		"task_type":   task.TaskType,
		"biz_id":      task.BizID,
		"status":      task.Status,
		"error_msg":   task.ErrorMsg,
		"retry_count": task.RetryCount,
	})
}

// ListTasks 分页查询当前租户的任务列表（管理员专属）
// GET /api/admin/task/list?page=1&page_size=10
// tenant_id 从 JWT 上下文拿，存储层强制租户过滤。
// 返回：{list: [...], total}，按 id 倒序（最新任务在前）。
//
// 用途：管理端查看本租户的文档处理等异步任务，观察处理进度与结果。
func ListTasks(c *gin.Context) {
	page, pageSize := parsePageGin(c)
	tenantID := middleware.GetTenantID(c)

	list, total, err := service.GetTaskList(c.Request.Context(), tenantID, page, pageSize)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	items := make([]gin.H, 0, len(list))
	for _, t := range list {
		items = append(items, gin.H{
			"id":          t.ID,
			"tenant_id":   t.TenantID,
			"task_type":   t.TaskType,
			"biz_id":      t.BizID,
			"status":      t.Status,
			"error_msg":   t.ErrorMsg,
			"retry_count": t.RetryCount,
			"created_at":  t.CreatedAt,
			"updated_at":  t.UpdatedAt,
		})
	}

	response.Success(c, gin.H{
		"list":  items,
		"total": total,
	})
}
