package toolkit

import "agent-platform/agent/interfaces"

// ============ Echo 测试工具 ============

// EchoTool 是一个用于验证骨架流程的测试工具。
// 它把收到的参数原样回显，不产生任何真实副作用，
// 用于验证"工具注册 → 引擎查找 → 统一执行"整条链路是通的。
type EchoTool struct{}

// NewEchoTool 构造一个 Echo 测试工具。
func NewEchoTool() *EchoTool {
	return &EchoTool{}
}

// Name 返回工具唯一标识。
func (EchoTool) Name() string { return "echo" }

// Description 返回工具描述，告诉 LLM 何时使用。
func (EchoTool) Description() string {
	return "回显用户输入的内容，用于测试（把参数原样返回）。"
}

// Parameters 返回参数说明（JSON Schema 格式）。
func (EchoTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"text": {
				"type": "string",
				"description": "要回显的文本"
			}
		},
		"required": ["text"]
	}`
}

// Execute 执行回显：直接把收到的参数原样返回。
func (t EchoTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	// 测试工具：直接把参数回显，不做任何解析与副作用。
	return params, nil
}
