package util

import "testing"

func TestCountTokensEmpty(t *testing.T) {
	if n := CountTokens(""); n != 0 {
		t.Fatalf("空文本应为 0，得到 %d", n)
	}
}

func TestCountTokensPureChinese(t *testing.T) {
	// "你好世界" = 4 个汉字 → 4*2 = 8
	if n := CountTokens("你好世界"); n != 8 {
		t.Fatalf("4个汉字应约8 token，得到 %d", n)
	}
}

func TestCountTokensPureEnglish(t *testing.T) {
	// "hello world" = 2 单词 → ceil(2*1.3)=ceil(2.6)=3
	if n := CountTokens("hello world"); n != 3 {
		t.Fatalf("2个英文词应约3 token，得到 %d", n)
	}
}

func TestCountTokensMixed(t *testing.T) {
	// "你好 world" = 2汉字(4) + 1词(ceil1.3=2) = 6
	if n := CountTokens("你好 world"); n != 6 {
		t.Fatalf("中文2+英文1词应约6 token，得到 %d", n)
	}
}

func TestCountTokensLongChineseText(t *testing.T) {
	// 100 个汉字的一满段中文 → 200
	long := ""
	for i := 0; i < 100; i++ {
		long += "汉"
	}
	if n := CountTokens(long); n != 200 {
		t.Fatalf("100汉字应约200 token，得到 %d", n)
	}
}

// TestCountTokensMagnitude 验证量级合理性：中文文本正常句子的估算不应离谱。
func TestCountTokensMagnitude(t *testing.T) {
	text := "今天天气很好，我们一起去公园散步吧。阳光明媚，微风徐徐。"
	n := CountTokens(text)
	// 手动估算：字数约 30，中文字约 27 → 约 54
	if n < 50 || n > 70 {
		t.Fatalf("量级异常，预期约54，得到 %d", n)
	}
}
