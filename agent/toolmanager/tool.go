package toolmanager

import "agent-platform/agent/interfaces"

// ============ 工具标准接口 ============

// Tool 是一个可被 Agent 调用的标准接口（插拔式工具统一规范）。
// 具体工具由 toolkit/ 等实现本接口后，注册到 ToolManager 即可被 ReAct 引擎调度。
//
// 为什么工具方法都以"字符串"收尾？
//   因为 LLM 只能理解文本。工具执行完把结果统一转成字符串喂回给 LLM，
//   让它继续思考下一步（Observe → Thought → Action 循环）。
//   工具内部返回什么类型（结构体/切片/数值）由工具自己决定，最后一律转成字符串即可。
type Tool interface {
	// Name 返回工具的唯一标识，如 "knowledge_retrieve"。
	// 引擎按此名字从 ToolManager 查找并调用对应工具。
	Name() string

	// Description 返回工具描述，说明"这个工具是干什么的、什么时候用"。
	// 该描述会被拼进给 LLM 的提示词里，帮助 LLM 判断何时调用本工具。
	Description() string

	// Parameters 返回参数说明（JSON Schema 格式）。
	// 告知 LLM 调用本工具需要传什么参数、各参数的类型和含义。
	Parameters() string

	// Execute 执行本工具，入参为调用方解析出的参数（字符串），
	// 返回执行结果（统一转成字符串，供 LLM 继续观察）。出错时返回错误。
	Execute(ctx interfaces.AgentContext, params string) (string, error)
}
