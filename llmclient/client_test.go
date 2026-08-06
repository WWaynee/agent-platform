package llmclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// ---------------------- 指数退避重试自测 ----------------------

// newRetryTestClient 构造一个注入小退避基数的测试客户端，
// 使 1s→2s→4s 的指数退避在毫秒级快速复现，便于验证"间隔递增"特征。
func newRetryTestClient(baseURL string, maxRetries int) *OpenAIClient {
	c := newTestClient(baseURL, time.Second, maxRetries)
	c.retryBase = 8 * time.Millisecond // 实际序列 8ms→16ms→32ms
	c.jitterRatio = 0                  // 关闭抖动，便于精确断言
	return c
}

// TestChat_Retry_500 自测点：接口持续返回 500，应重试 maxRetries 次（共请求 N+1 次），
// 且退避间隔递增（8ms→16ms→32ms，趋势等价于 1s→2s→4s）。
func TestChat_Retry_500(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newRetryTestClient(srv.URL, 3)

	start := time.Now()
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
	})
	elapsed := time.Since(start)

	// 应请求 1+3=4 次
	if got := count.Load(); got != 4 {
		t.Fatalf("期望重试 3 次共请求 4 次，实际请求 %d 次", got)
	}
	if err == nil {
		t.Fatal("500 持续失败应返回错误")
	}
	// 退避总和理论值 = 8+16+32 = 56ms，实际应不小于（未计抖动）
	if elapsed < 56*time.Millisecond {
		t.Fatalf("退避间隔未体现递增，耗时 %v 小于理论最小 56ms", elapsed)
	}
	t.Logf("✅ 500 重试 3 次: 总请求=%d, 耗时=%v, err=%v", count.Load(), elapsed, err)
}

// TestChat_Retry_401 自测点：接口返回 401 认证错误，属调用方错误，不应重试。
func TestChat_Retry_401(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := newRetryTestClient(srv.URL, 3)

	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
	})
	if got := count.Load(); got != 1 {
		t.Fatalf("401 不应重试，期望 1 次请求，实际 %d 次", got)
	}
	if err == nil {
		t.Fatal("401 应返回认证错误")
	}
	t.Logf("✅ 401 不重试: 请求次数=%d, err=%v", count.Load(), err)
}

// TestChat_Retry_TimeoutThenRecover 自测点：首次请求超时、随后服务恢复，
// 应触发重试并最终成功（证明超时错误可重试）。
func TestChat_Retry_TimeoutThenRecover(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if n == 1 {
			time.Sleep(80 * time.Millisecond) // 首次挂起，超过单次请求超时
		}
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	client := newRetryTestClient(srv.URL, 2)
	// 单次请求超时设小；整体 deadline 预算留足，允许"超时→退避→重试成功"
	client.httpClient.Timeout = 20 * time.Millisecond
	client.cfg.Timeout = time.Second

	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("期望重试后成功，实际错误: %v", err)
	}
	if got := count.Load(); got != 2 {
		t.Fatalf("期望首次超时后重试（共 2 次请求），实际 %d 次", got)
	}
	if resp == nil || resp.Content != "ok" {
		t.Fatalf("期望重试后拿到恢复的响应，实际 content=%+v", resp)
	}
	t.Logf("✅ 超时会重试并最终恢复: 请求次数=%d, content=%q", count.Load(), resp.Content)
}
