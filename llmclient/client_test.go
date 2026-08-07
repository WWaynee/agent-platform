package llmclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		retryBase:   time.Second,
		jitterRatio: 0.2,
		cb: NewCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 0.5,
			MinRequests:      5,
			Window:           10 * time.Second,
			OpenTimeout:      30 * time.Second,
		}),
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

// ---------------------- 简易熔断器自测 ----------------------

// fakeClock 手动推进的假时钟，用于让熔断状态机在毫秒级完成全流转验证。
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

// newCbWithClock 构造一个注入假时钟、最小评估参数（2次请求即评估）的熔断器。
func newCbWithClock() (*CircuitBreaker, *fakeClock) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 0.5,
		MinRequests:      2,
		Window:           10 * time.Second,
		OpenTimeout:      30 * time.Second,
	})
	cb.nowFunc = clk.now
	return cb, clk
}

// TestCb_OpenOnFailure 自测点：连续失败达到门槛 → 熔断器打开（Open）。
func TestCb_OpenOnFailure(t *testing.T) {
	cb, _ := newCbWithClock()

	// 2 次请求，全失败（失败率 100% > 50%）→ 打开
	cb.allow()
	cb.record(false)
	cb.allow()
	cb.record(false)

	if got := cb.State(); got != StateOpen {
		t.Fatalf("连续失败后应 Open，实际 %v", got)
	}
	// Open 后 allow 应拒绝（不发请求）
	if cb.allow() {
		t.Fatal("Open 状态仍放行请求，应直接拒绝")
	}
	t.Log("✅ 连续失败触发熔断打开，Open 下拒绝请求")
}

// TestCb_OpenToHalfOpen 自测点：熔断持续 openTimeout 后 → 进入半开，放一个试探请求。
func TestCb_OpenToHalfOpen(t *testing.T) {
	cb, clk := newCbWithClock()

	cb.allow()
	cb.record(false)
	cb.allow()
	cb.record(false) // 打开
	if cb.State() != StateOpen {
		t.Fatal("前置步骤应已 Open")
	}

	// 推进 30s（>= OpenTimeout）→ 半开
	clk.advance(30 * time.Second)
	if got := cb.State(); got != StateHalfOpen {
		t.Fatalf("熔断超时后应 Half-Open，实际 %v", got)
	}

	// 半开放第一个试探请求；第二个应被拒绝
	if !cb.allow() {
		t.Fatal("半开应放第一个试探请求")
	}
	if cb.allow() {
		t.Fatal("半开下第二个请求应被拒绝（只放一个试探）")
	}
	t.Log("✅ 熔断超时进入半开，仅放行一个试探请求")
}

// TestCb_HalfOpenSuccess_Close 自测点：半开试探成功后 → 回到 Closed。
func TestCb_HalfOpenSuccess_Close(t *testing.T) {
	cb, clk := newCbWithClock()

	cb.allow()
	cb.record(false)
	cb.allow()
	cb.record(false)
	clk.advance(30 * time.Second) // → Half-Open

	cb.allow()      // 放试探
	cb.record(true) // 试探成功
	if got := cb.State(); got != StateClosed {
		t.Fatalf("半开试探成功应回 Closed，实际 %v", got)
	}
	if !cb.allow() {
		t.Fatal("回 Closed 后应正常放行请求")
	}
	t.Log("✅ 半开试探成功 → 熔断关闭，恢复正常")
}

// TestCb_HalfOpenFail_StayOpen 自测点：半开试探失败后 → 继续 Open。
func TestCb_HalfOpenFail_StayOpen(t *testing.T) {
	cb, clk := newCbWithClock()

	cb.allow()
	cb.record(false)
	cb.allow()
	cb.record(false)
	clk.advance(30 * time.Second) // → Half-Open

	cb.allow()       // 放试探
	cb.record(false) // 试探失败
	if got := cb.State(); got != StateOpen {
		t.Fatalf("半开试探失败应回 Open，实际 %v", got)
	}
	if cb.allow() {
		t.Fatal("回 Open 后又放行，应继续拒绝")
	}
	t.Log("✅ 半开试探失败 → 继续熔断打开")
}

// ---------------------- 熔断器集成自测（业务无感知） ----------------------

// TestChat_CircuitBreaker_OpenNoHTTP 自测点：熔断打开后，调用 Chat
// 直接返回熔断错误且【不发 HTTP 请求】（保护下游），业务层无感知。
func TestChat_CircuitBreaker_OpenNoHTTP(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError) // 持续 500
	}))
	defer srv.Close()

	client := newRetryTestClient(srv.URL, 0) // maxRetries=0，每次 Chat=1 次请求，计数可预期
	// 注入小参数熔断器：2 次请求即评估，失败率 50% 触发
	client.cb = NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 0.5,
		MinRequests:      2,
		Window:           10 * time.Second,
		OpenTimeout:      30 * time.Second,
	})

	req := func() {
		_, _ = client.Chat(context.Background(), ChatRequest{
			Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
		})
	}

	// 2 次失败 → 打开熔断
	req()
	req()
	if got := client.cb.State(); got != StateOpen {
		t.Fatalf("连续失败后应 Open，实际 %v", got)
	}
	httpCountAfterOpen := count.Load()

	// Open 后再调用：应返回熔断错误且不再发 HTTP
	_, err := client.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
	})
	if err == nil || !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("Open 下应返回熔断错误，实际 err=%v", err)
	}
	if got := count.Load(); got != httpCountAfterOpen {
		t.Fatalf("Open 下不应再发 HTTP 请求，原 %d 现 %d", httpCountAfterOpen, got)
	}
	t.Logf("✅ 熔断打开后快速失败：HTTP 请求数保持 %d，返回 %v", count.Load(), err)
}

// TestChat_CircuitBreaker_Recover 自测点：熔断半开后试探成功 → 恢复正常（业务无感知切换）。
func TestChat_CircuitBreaker_Recover(t *testing.T) {
	var count atomic.Int32
	var failUntil atomic.Int32 // 前 N 次返回 500，之后返回成功
	failUntil.Store(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if int32(n) <= failUntil.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	client := newRetryTestClient(srv.URL, 0)
	client.cb = NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 0.5,
		MinRequests:      2,
		Window:           time.Second,
		OpenTimeout:      50 * time.Millisecond, // 快速半开
	})

	// 2 次失败 → Open
	for i := 0; i < 2; i++ {
		_, _ = client.Chat(context.Background(), ChatRequest{
			Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
		})
	}
	if client.cb.State() != StateOpen {
		t.Fatal("前置步骤应已 Open")
	}

	// 等待超过 openTimeout → 半开放试探；此时服务已恢复 → 试探成功回 Closed
	time.Sleep(80 * time.Millisecond)
	resp, err := client.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("半开试探应成功恢复，实际 err=%v", err)
	}
	if client.cb.State() != StateClosed {
		t.Fatalf("半开试探成功应回 Closed，实际 %v", client.cb.State())
	}
	if resp == nil || resp.Content != "ok" {
		t.Fatalf("应拿到恢复的响应，实际 content=%+v", resp)
	}
	t.Logf("✅ 熔断半开试探成功 → 自动恢复 closed，业务无感知")
}

// ---------------------- ChatWithJSON：JSON 输出校验与修复自测 ----------------------

// agentAction 模拟 Agent 需要解析的结构化 Action。
type agentAction struct {
	Action string                 `json:"action"`
	Tool   string                 `json:"tool"`
	Args   map[string]interface{} `json:"args"`
}

// newChatJSONServer 模拟 OpenAI 兼容 /chat/completions 端点：
// 把 contents 依次作为模型返回的 message.content（按顺序），并记录每次请求体。
func newChatJSONServer(contents []string, bodies *[]string) *httptest.Server {
	var i atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 记录请求体
		buf := new(bytes.Buffer)
		_, _ = io.Copy(buf, r.Body)
		if bodies != nil {
			*bodies = append(*bodies, buf.String())
		}
		idx := int(i.Load())
		content := contents[len(contents)-1]
		if idx < len(contents) {
			i.Add(1)
			content = contents[idx]
		}
		// 包装成 OpenAI chat.completions 响应（content 可能含 ```、换行等，需转义）
		contentJSON, _ := json.Marshal(content)
		fmt.Fprintln(w, fmt.Sprintf(`{"choices":[{"message":{"content":%s}}]}`, contentJSON))
	}))
}

func chatJSONReq() ChatRequest {
	return ChatRequest{Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}}}
}

// TestChatWithJSON_Valid 自测点：正常返回合法 JSON，解析成功，并注入了 system 约束。
func TestChatWithJSON_Valid(t *testing.T) {
	var bodies []string
	srv := newChatJSONServer([]string{
		`{"action":"search","tool":"web","args":{"q":"go"}}`,
	}, &bodies)
	defer srv.Close()

	client := newTestClient(srv.URL, time.Second, 0)

	var out agentAction
	if err := client.ChatWithJSON(context.Background(), chatJSONReq(), &out); err != nil {
		t.Fatalf("合法 JSON 应解析成功，实际 err=%v", err)
	}
	if out.Action != "search" || out.Tool != "web" {
		t.Fatalf("解析结果错误: %+v", out)
	}
	// 验证注入了严格 JSON 的 system 提示
	if len(bodies) == 0 || !strings.Contains(bodies[0], "你必须只返回严格的合法 JSON") {
		t.Fatalf("应注入严格 JSON system 提示，实际 body=%v", bodies)
	}
	t.Logf("✅ 正常合法 JSON 解析成功, out=%+v", out)
}

// TestChatWithJSON_CodeBlock 自测点：模型返回带 ```json 包裹，能自动剥离并解析成功。
func TestChatWithJSON_CodeBlock(t *testing.T) {
	srv := newChatJSONServer([]string{
		"```json\n{\"action\":\"search\",\"tool\":\"web\"}\n```",
	}, nil)
	defer srv.Close()

	client := newTestClient(srv.URL, time.Second, 0)

	var out agentAction
	if err := client.ChatWithJSON(context.Background(), chatJSONReq(), &out); err != nil {
		t.Fatalf("带 ```json 包裹应剥离解析成功，实际 err=%v", err)
	}
	if out.Action != "search" {
		t.Fatalf("解析结果错误: %+v", out)
	}
	t.Logf("✅ 自动剥离 ```json 代码块并解析成功, out=%+v", out)
}

// TestChatWithJSON_JunkRetry 自测点：完全乱的格式 → 重试一次（第二次返回合法 JSON）→ 成功。
func TestChatWithJSON_JunkRetry(t *testing.T) {
	var bodies []string
	srv := newChatJSONServer([]string{
		"一堆乱七八糟的文字 这不是JSON 也没有括号 {",
		`{"action":"search","tool":"web","args":{"q":"go"}}`,
	}, &bodies)
	defer srv.Close()

	client := newTestClient(srv.URL, time.Second, 0)

	var out agentAction
	if err := client.ChatWithJSON(context.Background(), chatJSONReq(), &out); err != nil {
		t.Fatalf("乱格式重试后应成功，实际 err=%v", err)
	}
	if out.Action != "search" {
		t.Fatalf("解析结果错误: %+v", out)
	}
	// 应请求 2 次（首次乱 + 重试），且重试请求带了"只返回 JSON"提示
	if len(bodies) != 2 {
		t.Fatalf("期望请求 2 次（含1次重试），实际 %d 次", len(bodies))
	}
	if !strings.Contains(bodies[1], "上一次返回的不是合法 JSON") {
		t.Fatalf("重试请求应含'只返回 JSON'提示，实际 body=%v", bodies[1])
	}
	t.Logf("✅ 乱格式重试一次后成功: 请求次数=%d, out=%+v", len(bodies), out)
}

// TestChatWithJSON_StillJunk 自测点：完全乱格式且重试后仍乱 → 返回 JSONFormatError。
func TestChatWithJSON_StillJunk(t *testing.T) {
	srv := newChatJSONServer([]string{
		"完全不是JSON 只是纯文字 没有任何括号结构",
		"还是乱的 依然是纯文本 无法构成任何json对象",
	}, nil)
	defer srv.Close()

	client := newTestClient(srv.URL, time.Second, 0)

	var out agentAction
	err := client.ChatWithJSON(context.Background(), chatJSONReq(), &out)
	if err == nil {
		t.Fatal("仍然乱格式应返回错误")
	}
	var jerr *JSONFormatError
	if !errors.As(err, &jerr) {
		t.Fatalf("期望返回 *JSONFormatError，实际 %T: %v", err, err)
	}
	if jerr.Raw == "" {
		t.Fatal("JSONFormatError 应携带原始内容用于排障")
	}
	t.Logf("✅ 重试后仍乱 → 返回格式错误: %v", err)
}
