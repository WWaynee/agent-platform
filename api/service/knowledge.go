package service

import (
	"context"
	"fmt"

	"agent-platform/config"
	"agent-platform/llmclient"
	"agent-platform/storage"
)

// ============ Service 层：知识库语义检索 ============
//
// 作用：根据一段自然语言问题（query），先从知识库（向量库 Qdrant）检索出最相关的
// 文档片段，供上层（如 ReAct Agent / RAG 对话接口）拼接上下文、生成回答。
//
// ⚠️ 多租户隔离：tenantID 以强参数传入，检索时传给 storage.SearchVectors，
//    由 storage 层在查询条件中「强制」加 tenant_id 过滤——即使本层（或更上层）
//    不小心传错，也绝不可能检索到别的租户的向量片段。隔离底线在 storage 死守。

// defaultSearchTopK 未指定 topK 时的默认返回条数。
const defaultSearchTopK = 3

// SearchHit 一条检索命中的文档片段（返回给上层/前端的结果单元）。
//   - Content:    命中的切片原文内容
//   - Score:      与 query 的相似度分数（Cosine，越高越相关，接近 1 最优）
//   - DocumentID: 命中片段所属文档 ID（便于溯源回跳文档）
//   - ChunkIndex: 片段在文档内的切片序号（从 0 开始）
type SearchHit struct {
	Content    string  `json:"content"`
	Score      float32 `json:"score"`
	DocumentID uint64  `json:"document_id"`
	ChunkIndex int     `json:"chunk_index"`
}

// Search 知识库语义检索：把 query 转成向量 → 在向量库中检索最相关的 topK 个片段。
//
// 入参：
//   - ctx：请求级上下文（携带 trace_id/tenant_id），透传给 Embedding 与向量检索，
//     使查询向量化/检索日志与整条链路共享同一 trace_id
//   - tenantID：当前租户 ID（**必须从 JWT 上下文传入**，多租户隔离关键）
//   - query：用户的自然语言问题/检索词
//   - topK：期望返回的片段条数；<=0 时使用默认值 3
//
// 逻辑：
//  1. 调用 Embedding 把 query 转成向量（与文档切片同模型，同 4096 维）
//  2. 调用 storage.SearchVectors 按 tenant_id 强制过滤检索（多租户隔离在 storage 层死守）
//  3. 把命中的原始片段组装成 SearchHit 列表返回
func Search(ctx context.Context, tenantID uint64, query string, topK int) ([]SearchHit, error) {
	if topK <= 0 {
		topK = defaultSearchTopK
	}

	// 1. query 转向量（复用与文档切片一致的双层客户端，留空自动回退主配置）
	llm := llmclient.NewClient(config.GlobalConfig.LLM)
	resp, err := llm.Embed(ctx, llmclient.EmbeddingRequest{Input: query})
	if err != nil {
		return nil, fmt.Errorf("查询向量化失败: %w", err)
	}
	if len(resp.Vector) == 0 {
		return nil, fmt.Errorf("查询向量化为空，无法检索")
	}

	// 2. 检索（storage 层内部用 tenantIDFilter 强制 tenant_id 过滤，多租户隔离底线）
	hits, err := storage.SearchVectors(ctx, resp.Vector, tenantID, topK)
	if err != nil {
		return nil, fmt.Errorf("向量检索失败: %w", err)
	}

	// 3. 组装结果（payload 中的 document_id/chunk_index 是 int64，这里统一转出来）
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHit{
			Content:    h.Content,
			Score:      h.Score,
			DocumentID: payloadUint64(h.Payload, "document_id"),
			ChunkIndex: int(payloadUint64(h.Payload, "chunk_index")),
		})
	}
	return out, nil
}

// payloadUint64 从 Qdrant 返回的 payload 里安全取出 uint64 字段。
// payload[key] 存的可能是 int64（Qdrant 整型）或 float64，统一按数值取。
// 取不到或类型不符时返回 0（检索结果只用于溯源展示，缺失不致命）。
func payloadUint64(payload map[string]interface{}, key string) uint64 {
	v, ok := payload[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return uint64(n)
	case int:
		return uint64(n)
	case float64:
		return uint64(n)
	case uint64:
		return n
	default:
		return 0
	}
}
