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

	return b.String()
}

// systemMessageFor 根据工具列表构造 engine 侧的 system 消息。
// 供引擎在组装 ChatRequest.Messages 时调用，把第一跳 message 填成 system。
func systemMessageFor(systemRole string, tools []toolmanager.Tool) Message {
	return Message{
		Role:    "system",
		Content: BuildSystemPrompt(systemRole, tools),
	}
}
