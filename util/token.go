package util

import (
	"math"
	"strings"
	"unicode"
)

// CountTokens 估算一段文本的 token 数量（粗略量级，用于判断是否超长即可）。
//
// 为什么不做精确计算：精确 token 数需要引入各厂商的 tokenizer 库，麻烦且没必要——
// 这里只用于判断"对话上下文是否快超出 LLM 的上下文窗口"，有个量级就够。
//
// 估算规则（保守取上限、宁多勿少，避免误判为"没超长"）：
//   - 中文（CJK）：1 个汉字 ≈ 2 个 token（经验值 1.5~2，取上限 2）
//   - 英文：1 个单词 ≈ 1.3 个 token（整体向上取整）
//   - 其余字符（标点、数字、符号等）按上面的英文规则一并计入，不单独重复
func CountTokens(text string) int {
	if text == "" {
		return 0
	}

	cjk := 0 // 中文（CJK）字符数
	var rest strings.Builder

	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			cjk++
		} else {
			rest.WriteRune(r)
		}
	}

	// 中文：每个汉字 2 token
	tokens := cjk * 2

	// 非中文部分按"英文单词"估算：1.3 token/词，整体向上取整
	// （strings.Fields 按空白切分，标点等会附着在相邻单词上，一并计入该词）
	wordCount := len(strings.Fields(rest.String()))
	tokens += int(math.Ceil(float64(wordCount) * 1.3))

	return tokens
}
