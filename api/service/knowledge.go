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

// maxSearchTopK 单次检索返回条数上限。
// ⚠️ 防止 LLM 在工具调用里传 topK=100 把大量片段一次性塞回上下文，撑爆 token。
const maxSearchTopK = 10

// SearchHit 一条检索命中的文档片段（返回给上层/前端的结果单元）。
//   - Content:      命中的切片原文内容
//   - Score:        与 query 的相似度分数（Cosine，越高越相关，接近 1 最优）
//   - DocumentID:   命中片段所属文档 ID（便于溯源回跳文档）
//   - DocumentName: 命中片段所属文档名称（LLM 据此引用来源，如"根据《xxx》…"）
//   - ChunkIndex:   片段在文档内的切片序号（从 0 开始）
type SearchHit struct {
	Content      string  `json:"content"`
	Score        float32 `json:"score"`
	DocumentID   uint64  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	ChunkIndex   int     `json:"chunk_index"`
}

// Search 知识库语义检索：把 query 转成向量 → 在向量库中检索最相关的 topK 个片段。
//
// 入参：
//   - ctx：请求级上下文（携带 trace_id/tenant_id），透传给 Embedding 与向量检索，
//     使查询向量化/检索日志与整条链路共享同一 trace_id
//   - tenantID：当前租户 ID（**必须从 JWT 上下文传入**，多租户隔离关键）
//   - query：用户的自然语言问题/检索词
//   - topK：期望返回的片段条数；本函数内统一收敛（<=0 → 默认 3；>10 → 压到 10）
//   - documentIDs：可选可变参数，指定只在这些文档里检索（`document_id in [...]`）；
//     本函数内统一净化（<=0 剔除；剔除后为空 → 退回全租户检索）
//
// ⚠️ 边界口径统一收敛入口：HTTP handler 与 Agent 工具都调本函数，top_k 的 clamp 与
// document_ids 的无效值剔除都在这里处理，避免两条入口各写一遍、口径不一。
//
// 逻辑：
//  1. ① top_k 收敛（<=0 → 默认3；>10 → 压到10）；② document_ids 净化（剔除 <=0 项）
//  2. 调用 Embedding 把 query 转成向量（与文档切片同模型，同 4096 维）
//  3. 调用 storage.SearchVectors 按 tenant_id 强制过滤 + document_id in 过滤（隔离在 storage 层死守）
//  4. 把命中的原始片段组装成 SearchHit（含 document_name）列表返回
func Search(ctx context.Context, tenantID uint64, query string, topK int, documentIDs ...uint64) ([]SearchHit, error) {
	// ① top_k 收敛 + ② document_ids 净化（边界统一收敛入口）
	topK, validIDs := normalizeSearchParams(topK, documentIDs)

	// 1. query 转向量（复用与文档切片一致的双层客户端，留空自动回退主配置）
	llm := llmclient.NewClient(config.GlobalConfig.LLM)
	resp, err := llm.Embed(ctx, llmclient.EmbeddingRequest{Input: query})
	if err != nil {
		return nil, fmt.Errorf("查询向量化失败: %w", err)
	}
	if len(resp.Vector) == 0 {
		return nil, fmt.Errorf("查询向量化为空，无法检索")
	}

	// 2. 检索（storage 层内部强制 tenant_id 过滤 + 可选 document_id in 过滤，多租户隔离底线）
	hits, err := storage.SearchVectors(ctx, resp.Vector, tenantID, topK, validIDs...)
	if err != nil {
		return nil, fmt.Errorf("向量检索失败: %w", err)
	}

	// 3. 组装结果（payload 中的 document_id/chunk_index 是 int64，这里统一转出来）
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHit{
			Content:      h.Content,
			Score:        h.Score,
			DocumentID:   payloadUint64(h.Payload, "document_id"),
			DocumentName: payloadString(h.Payload, "document_name"),
			ChunkIndex:   int(payloadUint64(h.Payload, "chunk_index")),
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

// payloadString 从 Qdrant 返回的 payload 里安全取出字符串字段。
// payload[key] 缺失或类型不符时返回空串（文章名缺失不致命，可回退用 document_id 反查）。
func payloadString(payload map[string]interface{}, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// normalizeSearchParams 统一收敛检索入参的边界（纯函数，HTTP 与 Agent 工具共用）。
//
//   - top_k：<=0 → 默认 defaultSearchTopK(3)；>maxSearchTopK(10) → 压到 10。
//     防止 LLM 传 100 把大量片段一次性塞回上下文、撑爆 token。
//   - document_ids：剔除 <=0 的项；剔除后为空视为"全租户检索"（等价未传）。
//
// 之所以提取成独立纯函数：边界口径若有变化只需改这一处，且可脱离外部依赖做单元自测。
func normalizeSearchParams(topK int, documentIDs []uint64) (int, []uint64) {
	// ① top_k 收敛
	if topK <= 0 {
		topK = defaultSearchTopK
	} else if topK > maxSearchTopK {
		topK = maxSearchTopK
	}

	// ② document_ids 净化：剔除 <=0 的项；剔除后为空说明调用方本意是"全租户"，按不传处理
	validIDs := make([]uint64, 0, len(documentIDs))
	for _, id := range documentIDs {
		if id > 0 {
			validIDs = append(validIDs, id)
		}
	}
	return topK, validIDs
}
