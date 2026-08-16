package toolkit

import (
	"strings"
	"testing"
)

// TestTruncateDocContent 覆盖需求单 5.2 的 get_document_content 超长截断：
// 超限截取前 MaxDocumentChars 字符、返回截断标记；未超限原样返回；中文按字符不切半。
func TestTruncateDocContent(t *testing.T) {
	t.Run("未超限：原样返回不截断", func(t *testing.T) {
		got, truncated := truncateDocContent("你好世界", 10)
		if truncated || got != "你好世界" {
			t.Errorf("未超限不应截断: %q truncated=%v", got, truncated)
		}
	})
	t.Run("恰好等于上限：不截断", func(t *testing.T) {
		s := strings.Repeat("a", 5)
		got, truncated := truncateDocContent(s, 5)
		if truncated || got != s {
			t.Errorf("长度恰等于上限不应截断，truncated=%v", truncated)
		}
	})
	t.Run("超限：截断到上限前 limit 字符", func(t *testing.T) {
		got, truncated := truncateDocContent(strings.Repeat("abc", 20), 30)
		if !truncated {
			t.Fatal("超限应标记截断")
		}
		if len(got) != 30 || got != strings.Repeat("abc", 10) {
			t.Errorf("应截到前 30 字符，实际 %q(len=%d)", got, len(got))
		}
	})
	t.Run("中文按字符截断不切半个字", func(t *testing.T) {
		// 中文字符都是多字节；按 []rune 截断不会把某个汉字切成半个字节。
		body := strings.Repeat("汉字甲", 2700) // 8100 个 rune > MaxDocumentChars(8000)
		got, truncated := truncateDocContent(body, MaxDocumentChars)
		if !truncated {
			t.Fatal("应标记截断")
		}
		// 截取后必须是合法 UTF-8 且是完整 rune 序列（长度恰为 MaxDocumentChars 个 rune）
		if len([]rune(got)) != MaxDocumentChars {
			t.Errorf("应截到 %d 个 rune，实际 %d", MaxDocumentChars, len([]rune(got)))
		}
		if !strings.HasPrefix(got, "汉字甲") {
			t.Errorf("截断应从开头保留完整汉字，实际前缀 %q", got[:6])
		}
	})
	t.Run("空串：不截断", func(t *testing.T) {
		got, truncated := truncateDocContent("", 5)
		if truncated || got != "" {
			t.Errorf("空串不应截断: %q truncated=%v", got, truncated)
		}
	})

	t.Log("✅ get_document_content 截断：超限截断/标记、中文按字符不切半、边界正确")
}
