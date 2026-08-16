package engine

import "strings"

// ============ 工具调用模板化摘要（热轨用） ============
//
// 把一次工具调用浓缩成一句可读中文（如 `[工具] knowledge_retrieve：检索到…`），
// 以 user 角色写进 Memory（热轨）。目的：
//   - 让下一轮 LLM 记得上轮调过什么工具、拿到什么结论；
//   - 同时参与超长自动压缩（热轨历史含工具背景）。
//
// ⚠️ 用代码模板生成，**零额外 LLM 调用**——快、可控、稳定。
// 真实 LLM 的 Summarize 只用于"整段历史压缩"，与此处单轮工具摘要无关。

// ToolSummaryMaxChars 工具调用一句话摘要的最大字符数（按 rune 截断，避免切碎中文。
// 超过即截断，防止把巨量工具结果塞进上下文）。
const ToolSummaryMaxChars = 80

// summarizeToolCall 把一次工具调用浓缩成一句可读中文摘要。
func summarizeToolCall(tr toolRecord) string {
	// 工具执行结果为空：视为未取得有效结果
	if strings.TrimSpace(tr.result) == "" {
		return "[工具] " + tr.call.ToolName + "：调用未返回有效结果"
	}
	// 工具执行失败（engine 里失败时会把错误文本拼成"工具 %q 执行失败: %v"）
	if strings.Contains(tr.result, "执行失败") {
		return "[工具] " + tr.call.ToolName + "：调用失败，未取得有效结果"
	}

	// 成功：把执行结果压成一句（去首尾空白 + 换行转分号 + 按 rune 截断）
	summary := strings.TrimSpace(tr.result)
	summary = strings.ReplaceAll(summary, "\n", "；")
	summary = cutRune(summary, ToolSummaryMaxChars)
	return "[工具] " + tr.call.ToolName + "：" + summary
}

// cutRune 按字符（rune）截断字符串，避免把多字节字符（中文/emoji）从中间切碎。
// 字符串长度 <= maxRunes 时原样返回。
func cutRune(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}
