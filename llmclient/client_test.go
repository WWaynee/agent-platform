package llmclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient 构造一个指向指定 baseURL、带指定超时/重试的测试客户端。
// 测试与 llmclient 同包，可直接构造内部结构。
func newTestClient(baseURL string, timeout time.Duration, maxRetries int) *OpenAIClient {
	return &OpenAIClient{
		cfg: LLMConfig{
			APIKey:         "test-key",
			BaseURL:        baseURL,
			ChatModel:      "test-model",
			EmbeddingModel: "test-embed",
			Timeout:        timeout,
			MaxRetries:     maxRetries,
		},
		httpClient: &http.Client{
			// 单次请求兜底超时
			Timeout: timeout,
		},
	}
}

// TestChat_Timeout_FastFail 自测点：把超时故意设成极小值（1ms），
// 调用 Chat 访问一个慢端点，应立即返回超时错误而不是卡住。
func TestChat_Timeout_FastFail(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"太慢了"}}]}`)
	}))
	defer slow.Close()

	client := newTestClient(slow.URL, 1*time.Millisecond, 3) // 1ms 超时，最多重试 3 次

	start := time.Now()
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("期望返回超时错误，实际却成功了（不应命中慢端点）")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("期望快速失败，实际耗时 %v 太长（可能发生了无谓的多轮挂起重试）", elapsed)
	}
	t.Logf("✅ 超时立即返回错误: err=%v, 耗时=%v", err, elapsed)
}

// TestChat_Timeout_NoCrash 自测点：超时场景下服务不崩溃（进程正常返回、未 panic）。
func TestChat_Timeout_NoCrash(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer slow.Close()

	client := newTestClient(slow.URL, 1*time.Millisecond, 2)

	for i := 0; i < 5; i++ {
		_, err := client.Chat(context.Background(), ChatRequest{
			Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
		})
		if err == nil {
			t.Fatalf("第 %d 次调用期望超时错误，实际成功", i)
		}
	}
	t.Log("✅ 多次超时调用均快速返回，服务未崩溃")
}
