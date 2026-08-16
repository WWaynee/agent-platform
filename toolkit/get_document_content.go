package toolkit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent-platform/agent/interfaces"
	"agent-platform/storage"
)

// ============ 文档全文读取工具 ============
//
// 作用：在 LLM 需要通读某篇文档（如"总结整篇《员工手册》""整理这份制度的所有条款"）时，
// 传入 document_id 返回该文档全文，供 LLM 基于完整内容作答。
// ⚠️ 必须按字符上限截断，并把"已截断 + 如需精确细节建议用 knowledge_retrieve"提示给 LLM，
// 避免整本塞爆上下文 / 超上下文窗口。

// MaxDocumentChars get_document_content 返回的全文最大字符数（按字符截断）。
// 估算：中文 1 字符 ≈ 1~1.5 token，取 8000 字符 ≈ 8000~12000 token，在常见上下文窗口内可控。
const MaxDocumentChars = 8000

// GetDocumentContentTool 把"读取文档全文"封装成 Agent 工具，供 ReAct 引擎调用。
type GetDocumentContentTool struct{}

// NewGetDocumentContentTool 构造文档全文读取工具。
func NewGetDocumentContentTool() *GetDocumentContentTool {
	return &GetDocumentContentTool{}
}

// Name 返回工具唯一标识。
func (GetDocumentContentTool) Name() string { return "get_document_content" }

// Description 返回工具描述，帮助 LLM 判断何时使用本工具。
func (GetDocumentContentTool) Description() string {
	return "读取某篇文档的完整内容。当用户明确要求基于某篇（或某几篇）文档的完整内容回答" +
		"（如'总结整篇''整理所有条款''通读后作答'），且你已通过 list_documents / search_documents 得知其文档ID时，使用本工具读全文。" +
		"参数 document_id 必填。返回文档全文（默认只返回前8000字符，超出会截断并提示改用 knowledge_retrieve 精确检索具体片段）。" +
		"注意：若用户只是问文档里的某个具体点，优先用 knowledge_retrieve 精确检索，而非读整篇全文。"
}

// Parameters 返回参数说明（JSON Schema 格式），告知 LLM 传什么参数。
func (GetDocumentContentTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"document_id": {
				"type": "integer",
				"description": "要读取全文的文档ID（通过 list_documents 或 search_documents 获取）"
			}
		},
		"required": ["document_id"]
	}`
}

// Execute 执行文档全文读取：带租户过滤读全文，按字符上限截断，超限给出提示。
func (t GetDocumentContentTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	// 1. 解析参数，拿到 document_id
	var req struct {
		DocumentID uint64 `json:"document_id"`
	}
	if err := json.Unmarshal([]byte(params), &req); err != nil {
		return "", fmt.Errorf("文档全文读取工具：参数解析失败，需传 JSON 含 document_id 字段: %w", err)
	}
	if req.DocumentID == 0 {
		return "", fmt.Errorf("文档全文读取工具：document_id 参数不能为空或 0")
	}

	// 2. 带租户过滤读取全文（storage 内部强制 tenant_id 过滤，防跨租户越权读他人文档）
	//    用 ctx.ToContext 把 Agent 上下文里的 trace_id/tenant/user 译成标准 ctx，贯穿到读取链路。
	docText, err := storage.ReadDocumentContent(ctx.ToContext(nil), ctx.TenantID, req.DocumentID)
	if err != nil {
		// 文档不存在 / 不属于当前租户 / MinIO 读取失败
		return "", fmt.Errorf("读取文档全文失败(document_id=%d): %w", req.DocumentID, err)
	}

	// 3. 按字符上限截断（超限 → 截取前 MaxDocumentChars 字符 + 置截断标记）
	content, truncated := truncateDocContent(docText.Content, MaxDocumentChars)

	// 4. 拼装返回，附带"原文总长 / 是否截断 / 来源 /（超长时优先展示已生成文档摘要）"信息
	var b strings.Builder
	fmt.Fprintf(&b, "文档《%s》(ID=%d) 全文如下：\n", docText.DocumentName, docText.DocumentID)
	b.WriteString(content)
	if truncated {
		// 超长：优先给 LLM 已预生成的文档摘要（若有），帮其先建立整体认知，再提示用检索拿精确片段
		if summ, ok := docSummary(ctx.ToContext(nil), ctx.TenantID, req.DocumentID); ok {
			fmt.Fprintf(&b, "\n\n（文档摘要：%s）", summ)
		}
		fmt.Fprintf(&b, "\n\n……（原文共 %d 字符，已截断至前 %d 字符。若需文档中某具体点的精确内容，"+
			"请用 knowledge_retrieve 工具按关键词检索该片段。）", docText.TotalChars, MaxDocumentChars)
	}
	return b.String(), nil
}

// docSummary 读取某篇文档已预生成的摘要（documents.summary），返回 (内容, 是否存在)。
// 供 get_document_content 超长截断时"优先返回摘要"；摘要未生成或读取失败时返回 ok=false。
// 带租户过滤读取，防止跨租户读他人文档摘要。
func docSummary(ctx context.Context, tenantID, documentID uint64) (string, bool) {
	doc, err := storage.GetDocumentByID(ctx, tenantID, documentID)
	if err != nil {
		return "", false
	}
	if doc.Summary == "" {
		return "", false
	}
	return doc.Summary, true
}

// truncateDocContent 按字符上限截断文档全文（纯函数，便于单测）。
// 返回 (截断后文本, 是否发生了截断)；字符串按 []rune 处理，避免把中文切出半个字符。
func truncateDocContent(content string, limit int) (string, bool) {
	runes := []rune(content)
	if len(runes) <= limit {
		return content, false
	}
	return string(runes[:limit]), true
}
