package engine

import (
	"fmt"
	"strings"

	"agent-platform/agent/toolmanager"
)

// ============ System Prompt 模板 ============
//
// ⚠️ Prompt 是 ReAct 能否工作的关键：LLM 完全依据这段提示来判断
// "要不要调用工具 / 按什么格式调用 / 什么时候直接回答"。
// 因此输出格式要求必须写得极清晰，并配正反示例，杜绝歧义。

// DefaultSystemRole 默认的角色设定文案，可被上层覆盖。
const DefaultSystemRole = "你是一个智能助手，能够综合运用企业内部知识库来回答用户的问题。"

// BuildSystemPrompt 根据角色设定与可用工具，组装完整的 System Prompt。
//
// 为什么把工具描述动态拼进来（而不是写死）：
//
//	工具是可插拔注册的，每次调用 BuildSystemPrompt 时从 ToolManager 实时取工具列表，
//	保证"Prompt 里描述的工具 == 实际注册可用的工具"，二者永远一致，
//	避免 LLM 被提示词诱导去调用一个不存在/未注册的工具。
//
// tools 参数：按工具名排序后的可用工具列表（来自 ToolManager.ListTools()）。
func BuildSystemPrompt(systemRole string, tools []toolmanager.Tool) string {
	var b strings.Builder

	// 1. 角色设定
	role := systemRole
	if strings.TrimSpace(role) == "" {
		role = DefaultSystemRole
	}
	b.WriteString(role)
	b.WriteString("\n\n")

	// 2. 可用工具列表
	b.WriteString("一、你可调用的工具如下（只能使用下面列出的工具）：\n")
	if len(tools) == 0 {
		b.WriteString("（当前没有可用工具，请直接基于你的知识回答，不要尝试调用任何工具。）\n")
	} else {
		for _, t := range tools {
			fmt.Fprintf(&b, "\n[工具名] %s\n", t.Name())
			fmt.Fprintf(&b, "[用途] %s\n", t.Description())
			b.WriteString("[参数格式(JSONSchema)]\n")
			b.WriteString(t.Parameters())
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	// 3. 输出格式要求（核心，必须极清晰）
	b.WriteString("二、输出格式要求（务必严格遵守）：\n")
	b.WriteString("你需要调用工具时，只输出下面这一条 JSON，不要有任何其他文字或解释：\n")
	b.WriteString(`{"action": "工具名", "action_input": "这里填工具参数(必须是JSON字符串对象)"}` + "\n")
	b.WriteString("你不需要调用工具、能直接回答时，只输出下面这条 JSON：\n")
	b.WriteString(`{"action": "final_answer", "action_input": "你的最终回答"}` + "\n\n")

	// 4. 格式示例（给正例，帮助 LLM 对齐）
	b.WriteString("三、正确示例：\n")
	b.WriteString("示例1（需要查知识库时）：\n")
	b.WriteString(`{"action": "knowledge_retrieve", "action_input": "{"query": "公司年假规定"}"}` + "\n")
	b.WriteString("示例2（知道答案，直接回答时）：\n")
	b.WriteString(`{"action": "final_answer", "action_input": "您好，根据我的了解，春节法定假日为3天。"}` + "\n\n")

	// 5. 规则约束
	b.WriteString("四、规则：\n")
	b.WriteString("1. 每次只输出一条 JSON，不要在里面混入任何解释性文字、空行或 Markdown 代码块。\n")
	b.WriteString("2. 只能使用第一部分列出的工具，不要虚构或调用未列出的工具。\n")
	b.WriteString("3. 如果不知道答案，请如实说'我不知道'，绝不编造不存在的知识或数据。\n")
	b.WriteString("4. action_input 中的参数必须是合法的 JSON 字符串（键值对用双引号）。\n")
	b.WriteString("5. 你的回答应基于检索到的知识库内容，引用时可指明来源，但不要声称访问过外部网站。\n")
	b.WriteString("\n")

	// 6. 文档维度检索指引（仅在对应文档检索工具已注册时追加，保证 Prompt 与实际工具一致）
	appendDocumentRetrieveGuide(&b, tools)

	return b.String()
}

// appendDocumentRetrieveGuide 追加"文档维度检索 / 多文档问题"的操作指引。
// 仅当 [list_documents / search_documents / get_document_content] 已注册时才写入对应段落，
// 避免提示词引导 LLM 调用一个未注册的工具。
//
// 目的（需求单0003）：让 LLM 面对"整文档级 / 多文档对比"问题时，先"知道有哪些文档 → 拿文档ID →
// 限定 document_ids 检索 / 必要时读全文"，而非盲目在全租户片段里搜。
func appendDocumentRetrieveGuide(b *strings.Builder, tools []toolmanager.Tool) {
	has := make(map[string]bool)
	for _, t := range tools {
		has[t.Name()] = true
	}

	// 仅当已具备"文档识别+限定检索"能力（list_documents 或 search_documents）时才写完整指引，
	// 避免提示词引导 LLM 调用一个未注册的工具。
	if !has["list_documents"] && !has["search_documents"] {
		return
	}

	b.WriteString("五、文档维度检索规则：\n")
	// 1. 用户提到具体文档名 → 先拿 ID，再限定检索
	b.WriteString("1. 当用户提到具体文档名称时（如'合同A'、'产品文档'、'员工手册'）：\n")
	b.WriteString("   a. 先用 list_documents（文档不多时）或 search_documents（文档多、需按名称搜时）拿到文档 ID；\n")
	if has["knowledge_retrieve"] {
		b.WriteString("   b. 再调 knowledge_retrieve 并传 document_ids 参数，限定只在相关文档里检索，减少噪声；\n")
	}
	b.WriteString("   c. 若你已在当前会话中调过 list_documents 且已得知文档列表，不要重复调用它——直接用已知的文档 ID 走 knowledge_retrieve；仅当你确实不知道某个文档 ID 时才重新查。\n")
	// 2. 匹配不到/匹配多个
	b.WriteString("2. 若用户提到的文档名匹配不到或匹配到多个：不要瞎猜、不要编造文档 ID；主动列出相近的文档（id+name）请用户确认'你指的是哪个？'；或在确实能覆盖时，不带 document_ids 全量检索。\n")
	// 3. 用户未提具体文档
	b.WriteString("3. 若用户未提具体文档（如'付款条款一般怎么写'）：可直接全量检索（不传 document_ids），或基于通用知识回答，不强求匹配文档。\n")
	// 4. get_document_content 使用边界
	if has["get_document_content"] {
		b.WriteString("4. get_document_content 仅用于以下情形，不要动不动就读全文：\n")
		b.WriteString("   a. 需要整篇文档的总结/核心观点提炼（如'总结文档A'）；b. 需完整上下文才能回答、片段不足以支撑；c. knowledge_retrieve 多次检索仍信息不足。\n")
		b.WriteString("   普通事实类问题（'某条款是什么'）优先用 knowledge_retrieve 精确检索，不要直接读全文。\n")
	} else {
		b.WriteString("4. 若需完整上下文，先以 knowledge_retrieve 多次精确检索覆盖各角度，而不是直接读全文。\n")
	}
	// 5. 引用来源 / 多文档区分
	b.WriteString("5. 回答中若引用文档内容，注明来源文档名称（如'根据《产品需求文档v2.0》……'）；多文档对比时分别标注各文档观点，不要混为一谈。\n")
	// 6. 文档多时优先 search
	if has["search_documents"] {
		b.WriteString("6. 文档多（list_documents 提示已超上限）时，优先用 search_documents 按名称精确搜索，不要依赖 list_documents 的全量列表。\n")
	}
	b.WriteString("--------\n")

	// 多文档对比/集成推理正例：先拿两份文档 ID → 限定 document_ids 分别检索 → 综合对比
	b.WriteString("多文档对比示例（问题：'对比《采购合同》与《销售合同A》在付款条款上的区别'）：\n")
	b.WriteString("先 list_documents（或 search_documents '合同'）拿到 采购合同(id=1)、销售合同A(id=2)；\n")
	b.WriteString("再 knowledge_retrieve {\"query\": \"付款条款\", \"document_ids\": [1,2]} 拿两篇的付款条款片段；\n")
	b.WriteString("观察返回片段综合对比，回答时分别标注'采购合同里说……销售合同A里说……'。\n")
}

// systemMessageFor 根据工具列表构造 engine 侧的 system 消息。
// 供引擎在组装 ChatRequest.Messages 时调用，把第一跳 message 填成 system。
func systemMessageFor(systemRole string, tools []toolmanager.Tool) Message {
	return Message{
		Role:    "system",
		Content: BuildSystemPrompt(systemRole, tools),
	}
}
