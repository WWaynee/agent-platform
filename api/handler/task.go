package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
)

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
	task, err := service.GetTaskDetail(tenantID, id)
	if err != nil {
		// 任务不存在/跨租户访问，统一返回 400（带出错提示）
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"id":         task.ID,
		"tenant_id":  task.TenantID,
		"task_type":  task.TaskType,
		"biz_id":     task.BizID,
		"status":     task.Status,
		"error_msg":  task.ErrorMsg,
		"retry_count": task.RetryCount,
	})
}
