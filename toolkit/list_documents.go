package toolkit

import (
	"fmt"
	"strings"

	"agent-platform/agent/interfaces"
	"agent-platform/storage"
)

// ============ 文档列表工具 ============
//
// 作用：让 LLM 先"知道当前租户有哪些文档"（名称 + ID）——这是"文档维度检索"编排的第一步。
// 当用户问"对比文档A和B""总结文档A""文档里提到的xx是什么"这类整文档级问题时，
// LLM 通常不知道有哪些文档、ID 是多少，本工具提供一个全量清单供其匹配合适的 document_id。

// maxListCount 单次返回文档条数上限。
// ⚠️ 防止文档很多时把全量清单一次性塞回上下文：只返回最多 maxListCount 篇，
// 超出部分提示 LLM 用 search_documents 按关键字精确检索（文档多时走搜索而非全量清单）。
const maxListCount = 50

// ListDocumentsTool 把"列出当前租户全部文档"封装成 Agent 工具，供 ReAct 引擎调用。
type ListDocumentsTool struct{}

// NewListDocumentsTool 构造文档列表工具。
func NewListDocumentsTool() *ListDocumentsTool {
	return &ListDocumentsTool{}
}

// Name 返回工具唯一标识。
func (ListDocumentsTool) Name() string { return "list_documents" }

// Description 返回工具描述，帮助 LLM 判断何时使用本工具。
func (ListDocumentsTool) Description() string {
	return "列出当前企业知识库里的全部文档（文档名称 + 文档ID）。" +
		"当用户的问题涉及具体某个/某几个文档（如'总结《产品需求》''对比文档《a》和《b》'）时，先调用本工具得知有哪些文档及各自ID，" +
		"再配合 knowledge_retrieve（传 document_ids）或 get_document_content（传 document_id）完成下游操作。" +
		"返回最多50篇；若文档超过50篇会提示改用 search_documents 按名称精确检索。"
}

// Parameters 返回参数说明（无参数，返回空 JSON 对象约束）。
func (ListDocumentsTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {}
	}`
}

// Execute 执行文档列表：返回当前租户的文档清单（名称 + ID + 大小 + 状态）。
func (t ListDocumentsTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	// 1. keyword 传空 = 返回该租户全部文档（storage.SearchDocuments 空关键字时不分页全量返回）
	//    用 ctx.ToContext 把 Agent 上下文里的 trace_id/tenant/user 译成标准 ctx，贯穿到 DB 查询链路。
	docs, err := storage.SearchDocuments(ctx.ToContext(nil), ctx.TenantID, "")
	if err != nil {
		return "", fmt.Errorf("列出文档失败: %w", err)
	}

	// 2. 没有文档
	if len(docs) == 0 {
		return "当前企业知识库中还没有任何文档。", nil
	}

	// 3. 拼装清单（名称 + 文档ID，供 LLM 后续引用 document_id）
	var b strings.Builder
	fmt.Fprintf(&b, "当前企业知识库共 %d 篇文档（最多展示 %d 篇）：\n", len(docs), maxListCount)
	for i, d := range docs {
		if i >= maxListCount {
			fmt.Fprintf(&b, "\n（已列出前 %d 篇，文档较多；可用 search_documents 按名称关键字精确检索后拿 id）", maxListCount)
			break
		}
		// 携带时间/ID/名称/大小/状态/摘要，便于 LLM 判断文档新鲜度、筛选并快速了解内容
		fmt.Fprintf(&b, "- 文档ID=%d  %s（大小 %d 字节，状态 %s，更新于 %s）\n",
			d.ID, d.Name, d.Size, d.Status, d.UpdatedAt.Format("2006-01-02 15:04"))
		if d.Summary != "" {
			fmt.Fprintf(&b, "  摘要：%s\n", d.Summary)
		}
	}
	return b.String(), nil
}
