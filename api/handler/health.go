package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"agent-platform/api/service"
)

// ============ 详细健康检查接口 ============
//
// GET /health
// 返回所有依赖（MySQL / Redis / MinIO / Qdrant / RabbitMQ）的健康状态。
//   - 全部 up：HTTP 200，overall=up，各组件 status=up
//   - 任一 down：HTTP 503，overall=down，对应组件带 error 信息
//
// 为什么要用 HTTP 状态码区分：K8s / Docker 探针（liveness/readiness）就是靠 HTTP
// 状态码判断实例是否可用 —— 200 存活且就绪；503 表示有依赖异常，应被编排系统剔除/重启。
// 若整体 down 仍返回 200，负载均衡就不会摘掉故障实例，请求仍会被路由进来导致更多 5xx。
func Health(c *gin.Context) {
	report := service.CheckAll()

	if report.IsHealthy() {
		c.JSON(http.StatusOK, report)
		return
	}
	c.JSON(http.StatusServiceUnavailable, report)
}
