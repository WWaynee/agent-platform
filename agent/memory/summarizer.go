package memory

// ============ 摘要生成能力（依赖注入） ============
//
// Memory 需要"超长自动压缩"，而压缩要生成摘要——生成摘要需要 LLM。
// 但 Memory 层不应反向依赖 engine / llmclient（保持单向依赖、职责单一）。
// 因此在这里定义最小接口 Summarizer，由外部（engine / 启动装配）实现并注入。
type Summarizer interface {
	// Summarize 把一段历史对话凝练成一段摘要文本。
	// 实现通常接 LLM；测试可注入 mock。
	Summarize(msgs []ChatMessage) (string, error)
}
