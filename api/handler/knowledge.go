package handler

import (
	"github.com/gin-gonic/gin"

	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
	"agent-platform/api/validator"
)

// ============ 知识库检索测试接口 ============
//
// 作用：把 service.Search 暴露成 HTTP 接口，方便调试/联调/给前端用。
// 传一段查询文本，按当前租户的知识库检索出最相关的文档片段返回。
//
// ⚠️ 多租户：tenant_id 一律从 JWT 上下文拿（middleware.GetTenantID），
//    绝不信任前端传的；检索过滤仍是 storage 层强制，本层负责拿到正确来源。

// SearchRequest 知识库检索请求（JSON body）
type SearchRequest struct {
	Query string `json:"query" binding:"required,min=1,max=500"` // 查询文本（必填，长度 1~500）
	TopK  int    `json:"top_k"`                                  // 期望返回的片段条数；<=0 时服务层默认取 3
}

// KnowledgeSearch 知识库检索
// POST /api/knowledge/search
//
// 请求体：{"query": "问题", "top_k": 3}
// 返回：命中的文档片段列表（content / score / document_id / chunk_index）
func KnowledgeSearch(c *gin.Context) {
	// 1. 从 JWT 上下文拿当前租户（唯一可信来源）
	tenantID := middleware.GetTenantID(c)
	if tenantID == 0 {
		response.Unauthorized(c, "未获取到租户信息")
		return
	}

	// 2. 解析请求体（标签校验：query 必填）
	var req SearchRequest
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	// 3. 调用检索 service
	hits, err := service.Search(tenantID, req.Query, req.TopK)
	if err != nil {
		response.ServerError(c, err.Error())
		return
	}

	// 4. 返回结果
	response.Success(c, gin.H{
		"query":     req.Query,
		"top_k":     len(hits),
		"tenant_id": tenantID,
		"results":   hits,
	})
}
