package toolkit

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-platform/agent/interfaces"
	"agent-platform/api/service"
)

// ============ 知识库检索工具（RAG 核心工具） ============

// KnowledgeRetrieveTool 把企业知识库（RAG）语义检索封装成 Agent 工具，
// 让 ReAct 引擎在回答涉及企业内部资料、规章制度、产品说明等私有知识时，
// 能调用本工具检索出相关文档片段作为回答依据。
//
// ⚠️ 多租户隔离：执行时从 AgentContext.TenantID 取当前租户，
//    传给 service.Search 检索，隔离底线仍在 storage 层（SearchVectors 强制 tenant_id 过滤）。
type KnowledgeRetrieveTool struct{}

// NewKnowledgeRetrieveTool 构造知识库检索工具。
func NewKnowledgeRetrieveTool() *KnowledgeRetrieveTool {
	return &KnowledgeRetrieveTool{}
}

// Name 返回工具唯一标识。
func (KnowledgeRetrieveTool) Name() string { return "knowledge_retrieve" }

// Description 返回工具描述，帮助 LLM 判断何时使用本工具。
//
// ⚠️ 描述质量直接决定 LLM 调用准确性：LLM 仅凭这段文本来决定是否调用，
// 必须写清楚"什么时候用 / 参数 / 返回"，并给出明确的触发场景与反例。
func (KnowledgeRetrieveTool) Description() string {
	return "知识库检索工具：用于查询企业内部文档、规章制度、产品说明、机密资料等私有知识。" +
		"当用户的问题涉及内部资料、公司规定、具体文档内容、需要事实依据时，必须使用此工具。" +
		"参数 query 为要查询的问题或关键词。返回与问题最相关的若干条文档片段及其来源。" +
		"注意：此工具不使用大模型参数，仅用于检索企业已上传的知识库，可多次调用以覆盖不同的查询角度。"
}

// Parameters 返回参数说明（JSON Schema 格式），告知 LLM 传什么参数。
func (KnowledgeRetrieveTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "要检索的问题或关键词，尽量说明确、具体，以获得准确结果"
			}
		},
		"required": ["query"]
	}`
}

// Execute 执行知识库检索。
// 入参 params 是 LLM 生成的 JSON 字符串（含 query 字段），
// 出参是把检索命中的文档片段拼接成的字符串，供 LLM 继续观察作答。
func (t KnowledgeRetrieveTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	// 1. 解析参数，拿到 query
	var req struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(params), &req); err != nil {
		return "", fmt.Errorf("知识库检索工具：参数解析失败，需传 JSON 含 query 字段: %w", err)
	}
	if strings.TrimSpace(req.Query) == "" {
		return "", fmt.Errorf("知识库检索工具：query 参数不能为空")
	}

	// 2. 以当前租户身份调用 service.Search 检索（topK 取 3）
	hits, err := service.Search(ctx.TenantID, req.Query, 3)
	if err != nil {
		return "", fmt.Errorf("知识库检索失败: %w", err)
	}

	// 3. 若无命中，给出明确提示（LLM 据此判断知识库里没有相关内容）
	if len(hits) == 0 {
		return "知识库检索没有返回任何结果：在当前企业知识库中未找到与问题相关的文档片段。", nil
	}

	// 4. 拼接命中片段成易读文本
	var b strings.Builder
	fmt.Fprintf(&b, "知识库检索结果（共 %d 条，按相关度从高到低）：\n", len(hits))
	for i, h := range hits {
		// 为便于 LLM 引用，每条标明序号、来源文档与相关度
		fmt.Fprintf(&b, "[%d] 文档ID=%d 相关度=%.3f\n%s\n\n", i+1, h.DocumentID, h.Score, h.Content)
	}
	return b.String(), nil
}
