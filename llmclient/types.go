package llmclient

// ============ 消息结构体 ============

// 角色常量：与主流大模型（OpenAI 兼容接口）对齐
const (
	RoleSystem    = "system"    // 系统提示词，设定助手人设/规则
	RoleUser      = "user"      // 用户输入
	RoleAssistant = "assistant" // 助手回复
	RoleTool      = "tool"      // 工具执行结果（ReAct 阶段使用）
)

// ChatMessage 对话消息
// 一组 messages 组成一次完整对话上下文（system 开场 + 多轮 user/assistant 交替）
type ChatMessage struct {
	Role    string `json:"role"`           // system / user / assistant / tool
	Content string `json:"content"`        // 消息文本内容
	Name    string `json:"name,omitempty"` // 可选：消息作者名（tool 场景用）
}

// ============ 请求结构体 ============

// ChatRequest 对话请求
type ChatRequest struct {
	Messages    []ChatMessage `json:"messages"`    // 对话消息列表（含上下文）
	Temperature float64       `json:"temperature"` // 采样温度，0~2，越高越随机
	MaxTokens   int           `json:"max_tokens"`  // 生成的最大 token 数
	// Stream 是否流式输出。本阶段前端用不上，暂置 false（预留字段）
	Stream bool `json:"stream"`
}

// EmbeddingRequest 向量生成请求
// 输入一段文本，返回其向量表示（用于 RAG 检索）
type EmbeddingRequest struct {
	Input string `json:"input"` // 输入文本
}

// ============ Token 用量 ============

// TokenUsage token 用量统计
// 与 OpenAI 兼容接口的 usage 结构对齐
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`     // 请求（输入）消耗 token
	CompletionTokens int `json:"completion_tokens"` // 生成（输出）消耗 token
	TotalTokens      int `json:"total_tokens"`      // 总消耗
}

// ============ 响应结构体 ============

// ChatResponse 对话响应
type ChatResponse struct {
	Content string     // 助手回复文本
	Usage   TokenUsage // token 用量统计
}

// EmbeddingResponse 向量生成响应
type EmbeddingResponse struct {
	Vector []float32  // 输入文本的向量表示（维度由模型决定）
	Usage  TokenUsage // token 用量统计
}
