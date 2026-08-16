package toolkit

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-platform/agent/interfaces"
	"agent-platform/storage"
)

// ============ 文档名称搜索工具 ============
//
// 作用：在"文档很多、list_documents 全量清单过长"时，让 LLM 先按名称关键字精确检索出
// 目标文档及其 ID，再配合 knowledge_retrieve / get_document_content 完成下游操作。
// 语义上这是"名称→ID 解析"的 B 路径（文档多时用搜索替代全量清单）。

// maxSearchCount 单次返回的文档条数上限。
// ⚠️ 防止命中过多塞爆上下文；超过上限提示继续换关键词缩小范围。
const maxSearchCount = 20

// SearchDocumentsTool 把"按文档名称关键字搜索"封装成 Agent 工具，供 ReAct 引擎调用。
type SearchDocumentsTool struct{}

// NewSearchDocumentsTool 构造文档名称搜索工具。
func NewSearchDocumentsTool() *SearchDocumentsTool {
	return &SearchDocumentsTool{}
}

// Name 返回工具唯一标识。
func (SearchDocumentsTool) Name() string { return "search_documents" }

// Description 返回工具描述，帮助 LLM 判断何时使用本工具。
func (SearchDocumentsTool) Description() string {
	return "按名称关键字搜索企业知识库里的文档（返回匹配文档的名称 + 文档ID）。" +
		"当文档很多、list_documents 反馈'文档较多'时，或你已大概知道文档名称、想拿到其精确文档ID时，使用本工具按名称关键字搜索。" +
		"参数 keyword 为文档名称中的关键字（如'需求''制度'），返回名称含该关键字的文档及其ID，供后续 knowledge_retrieve / get_document_content 引用。"
}

// Parameters 返回参数说明（JSON Schema 格式），告知 LLM 传什么参数。
func (SearchDocumentsTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"keyword": {
				"type": "string",
				"description": "文档名称中的搜索关键字，尽量准确、简短（如'需求'、'员工制度'）"
			}
		},
		"required": ["keyword"]
	}`
}

// Execute 执行按名称搜索：返回名称含 keyword 的文档清单（名称 + ID）。
func (t SearchDocumentsTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	// 1. 解析参数，拿到 keyword
	var req struct {
		Keyword string `json:"keyword"`
	}
	if err := json.Unmarshal([]byte(params), &req); err != nil {
		return "", fmt.Errorf("文档名称搜索工具：参数解析失败，需传 JSON 含 keyword 字段: %w", err)
	}
	keyword := strings.TrimSpace(req.Keyword)
	if keyword == "" {
		return "", fmt.Errorf("文档名称搜索工具：keyword 参数不能为空")
	}

	// 2. 带租户过滤按名称模糊搜索（storage.SearchDocuments 用参数化 LIKE，天然防注入）
	//    用 ctx.ToContext 把 Agent 上下文里的 trace_id/tenant/user 译成标准 ctx，贯穿到 DB 链路。
	docs, err := storage.SearchDocuments(ctx.ToContext(nil), ctx.TenantID, keyword)
	if err != nil {
		return "", fmt.Errorf("文档名称搜索失败: %w", err)
	}

	// 3. 无命中
	if len(docs) == 0 {
		return fmt.Sprintf("没有找到名称包含 %q 的文档，可尝试换关键词再搜或先 list_documents 看全部。", keyword), nil
	}

	// 4. 拼装命中清单
	var b strings.Builder
	fmt.Fprintf(&b, "匹配到 %d 篇名称含 %q 的文档（最多展示 %d 篇）：\n", len(docs), keyword, maxSearchCount)
	for i, d := range docs {
		if i >= maxSearchCount {
			fmt.Fprintf(&b, "\n（命中较多，建议用更精确的关键词收窄）")
			break
		}
		fmt.Fprintf(&b, "- 文档ID=%d  %s（更新于 %s）\n", d.ID, d.Name, d.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return b.String(), nil
}
