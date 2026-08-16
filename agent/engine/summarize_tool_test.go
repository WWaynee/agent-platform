package engine

import (
	"strings"
	"testing"
)

// 覆盖 summarizeToolCall / toolParamsJSON / cutRune 模板化摘要逻辑（零 LLM 调用）。

func TestSummarizeToolCall(t *testing.T) {
	cases := []struct {
		name   string
		tr     toolRecord
		expect []string // 任一子串命中即可（校验关键语义）
		notIn  []string // 不应出现的子串
	}{
		{
			name: "成功-短结果",
			tr: toolRecord{
				call:   ToolCall{ToolName: "knowledge_retrieve"},
				result: "检索命中：2024 年销售额 1200 万。",
			},
			expect: []string{"[工具] knowledge_retrieve：检索命中：2024 年销售额 1200 万。"},
		},
		{
			name: "成功-长结果截断到80字",
			tr: toolRecord{
				call:   ToolCall{ToolName: "knowledge_retrieve"},
				result: strings.Repeat("命", 100),
			},
			expect: []string{"[工具] knowledge_retrieve：" + strings.Repeat("命", ToolSummaryMaxChars)},
			notIn:  []string{strings.Repeat("命", ToolSummaryMaxChars+1)},
		},
		{
			name: "多行结果合并为一段",
			tr: toolRecord{
				call:   ToolCall{ToolName: "web_search"},
				result: "第一行结论\n第二行结论",
			},
			expect: []string{"；"}, // 换行已转分号
			notIn:  []string{"\n"},
		},
		{
			name: "执行失败标记",
			tr: toolRecord{
				call:   ToolCall{ToolName: "knowledge_retrieve"},
				result: "工具 \"knowledge_retrieve\" 执行失败: timeout",
			},
			expect: []string{"调用失败"},
		},
		{
			name: "空结果视为未取得有效结果",
			tr: toolRecord{
				call:   ToolCall{ToolName: "web_search"},
				result: "   ",
			},
			expect: []string{"未返回有效结果"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarizeToolCall(c.tr)
			for _, exp := range c.expect {
				if !strings.Contains(got, exp) {
					t.Errorf("期望包含 %q，实际 %q", exp, got)
				}
			}
			for _, n := range c.notIn {
				if strings.Contains(got, n) {
					t.Errorf("不应包含 %q，实际 %q", n, got)
				}
			}
		})
	}
}

func TestCutRuneDoesNotSplitMultiByte(t *testing.T) {
	in := "中文😀abcd"
	max := 4
	got := cutRune(in, max)
	if want := "中文😀a"; got != want {
		// 期望前 4 个 rune：中/文/😀/a（emoji 是单 rune，不应被切在中间）
		t.Errorf("cutRune(%q,%d) 期望 %q，实际 %q（按 rune 截断异常）", in, max, want, got)
	}
	// 边界：max 超过长度应原样
	if got := cutRune(in, 100); got != in {
		t.Errorf("cutRune(max超长) 应原样返回，实际 %q", got)
	}
}

func TestToolParamsJSON(t *testing.T) {
	got := toolParamsJSON(map[string]any{"query": "销售额"})
	if !strings.Contains(got, "销售额") {
		t.Errorf("toolParamsJSON 应包含参数值，实际 %q", got)
	}
	// 空 map 返回空串
	if toolParamsJSON(nil) != "" {
		t.Errorf("空参数应返回空串")
	}
}
