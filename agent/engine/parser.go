package engine

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ============ LLM 输出解析器 ============
//
// LLM 的返回可能带 ```json 包裹、前后多余文字、或残缺 JSON。
// 如果解析失败，Agent 会中断，因此这里做多层容错，尽可能"救回来"。
// 解析只是"尽力而为"，代价便宜：宁可多试几次，也不让一次格式不整导致 Agent 崩溃。

// ParsedOutput 是 LLM 输出解析后的结构化结果。
//   - Action：工具名；若为 final_answer 表示直接回答（不需要再调工具）。
//   - Input  ：action_input 的原文（字符串）。
//     对工具调用，它是工具参数（JSON 字符串，如 `{"query":"..."}`）；
//     对 final_answer，它是给用户的回答文本。
type ParsedOutput struct {
	Action string `json:"action"`
	Input  string `json:"action_input"`
}

// parseLLMOutput 解析 LLM 返回的文本，提取 {action, action_input}。
// 容错自上而下逐层降级：
//  1. 整段直接当 JSON 解析；
//  2. 若有 ```json ... ``` 围栏，先剥离围栏再解析；
//  3. 取第一个 '{' 与最后一个 '}' 之间的子串再解析（容忍前后多余说明文字）；
//  4. 3 层都失败 → 返回明确错误（不 panic）。
func parseLLMOutput(output string) (*ParsedOutput, error) {
	if strings.TrimSpace(output) == "" {
		return nil, fmt.Errorf("解析 LLM 输出失败：输出为空")
	}

	// 容错①②③：尝试多份 JSON 候选文本，依次解析
	candidates := candidateJSONStrings(output)
	var lastErr error
	for _, c := range candidates {
		var p ParsedOutput
		if err := json.Unmarshal([]byte(c), &p); err != nil {
			lastErr = err
			continue
		}
		// 必须要有 action，否则视为非法输出
		if strings.TrimSpace(p.Action) == "" {
			lastErr = fmt.Errorf("解析失败：JSON 中缺少 action 字段")
			continue
		}
		return &p, nil
	}

	return nil, fmt.Errorf("解析 LLM 输出失败：无法提取出合法的 {action, action_input} JSON（最后错误: %v）", lastErr)
}

// candidateJSONStrings 产出若干"待尝试解析"的 JSON 字符串，按可修复程度升序排列。
//  1. 原文（可能本身就是个干净 JSON）
//  2. 去掉 ```json ··· ``` 围栏（含 json 或纯 ```）
//  3. 截取第一个 '{' 到最后一个 '}' 之间的子串
func candidateJSONStrings(output string) []string {
	var cands []string

	// 候选1：原文
	raw := strings.TrimSpace(output)
	if raw != "" {
		cands = append(cands, raw)
	}

	// 候选2：剥离代码围栏
	if fenced := stripCodeFence(raw); fenced != "" && fenced != raw {
		cands = append(cands, fenced)
	}

	// 候选3：截取 { ... } 之间的内容（容忍前后多余文字）
	if cut := extractJSONObject(raw); cut != "" && cut != raw {
		cands = append(cands, cut)
	}

	return cands
}

// stripCodeFence 尝试去掉 ```json ... ``` 或 ``` ... ``` 围栏。
// 找到首个 ``` 与其后最近一个 ``` 之间的内容；找不到则返回空串。
func stripCodeFence(s string) string {
	start := strings.Index(s, "```")
	if start < 0 {
		return ""
	}
	// 跳过 ``` 以及紧随其后的语言标识（如 json、plaintext）
	afterOpen := start + 3
	// 去掉本行剩余内容（语言标签）
	if nl := strings.IndexByte(s[afterOpen:], '\n'); nl >= 0 {
		afterOpen += nl + 1
	}
	close := strings.Index(s[afterOpen:], "```")
	if close < 0 {
		return ""
	}
	body := s[afterOpen : afterOpen+close]
	return strings.TrimSpace(body)
}

// extractJSONObject 截取字符串中第一个 '{' 到最后一个 '}' 之间的子串。
// 用于"前后有说明文字"的场景；找不到大括号则返回空串。
func extractJSONObject(s string) string {
	open := strings.IndexByte(s, '{')
	close := strings.LastIndexByte(s, '}')
	if open < 0 || close < 0 || close <= open {
		return ""
	}
	return s[open : close+1]
}

// ParseLLMOutput 是 parseLLMOutput 的导出版本，便于外部调用与单元测试。
func ParseLLMOutput(output string) (*ParsedOutput, error) {
	return parseLLMOutput(output)
}

// rawInputToParams 把工具传入的参数字符串转成 map，供 ToolCall 审计记录。
// 参数通常是 JSON 对象字符串（如 `{"query":"..."}`），解析失败时降级为原始文本占位。
func rawInputToParams(input string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(input), &m); err == nil && m != nil {
		return m
	}
	return map[string]any{"__raw": input}
}
