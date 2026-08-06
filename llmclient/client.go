package llmclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"agent-platform/config"
)

// ============ 面向接口设计 ============

// Client 是 LLM 客户端接口。
// ⚠️ 核心原则：业务层（agent / RAG / service）只依赖这个接口，
// 不依赖具体厂商实现。以后换模型厂商（OpenAI/DeepSeek/通义...），
// 只需新增一个实现类型，业务层代码零改动。
type Client interface {
	// Chat 发送对话请求，返回助手回复
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// Embed 将文本转为向量（用于 RAG 检索）
	Embed(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error)
}

// ============ OpenAI 兼容实现 ============

// OpenAIClient 基于 OpenAI 兼容 /chat/completions 与 /embeddings 接口实现。
// DeepSeek 等国内模型大多兼容 OpenAI 协议，可直接复用。
type OpenAIClient struct {
	cfg        LLMConfig    // 客户端配置（从 config.LLM 复制过来）
	httpClient *http.Client // 复用连接的 HTTP 客户端

	// 指数退避参数：默认 1s 起步、1s→2s→4s；加 20% 随机抖动避免雪崩。
	// retryBase 仅供测试注入更小基数以快速验证；jitterRatio 为抖动比例（0.2 = 20%）。
	retryBase   time.Duration
	jitterRatio float64
}

// LLMConfig 客户端内部使用的配置（从全局 config 拷贝，避免后续全局改动影响已建实例）
type LLMConfig struct {
	APIKey         string
	BaseURL        string
	ChatModel      string
	EmbeddingModel string
	EmbedAPIKey    string // 向量服务独立 key（为空回退用 APIKey）
	EmbedBaseURL   string // 向量服务独立地址（为空回退用 BaseURL）
	Timeout        time.Duration
	MaxRetries     int
}

// NewClient 构造函数：接收业务层的 llm 配置与全局配置，返回 Client 接口。
// 这里从 config.GlobalConfig.LLM 读取，并实例化带超时的 HTTP 客户端。
// 注：超时控制采用「总预算 + 每轮 request context」策略，
// 故 httpClient 不设固定 Timeout（统一由 doPost 里的 deadline 控制）。
func NewClient(llm config.LLMConfig) Client {
	return &OpenAIClient{
		cfg: LLMConfig{
			APIKey:         llm.APIKey,
			BaseURL:        llm.BaseURL,
			ChatModel:      llm.ChatModel,
			EmbeddingModel: llm.EmbeddingModel,
			EmbedAPIKey:    llm.EmbedAPIKey,
			EmbedBaseURL:   llm.EmbedBaseURL,
			Timeout:        time.Duration(llm.Timeout) * time.Second,
			MaxRetries:     llm.MaxRetries,
		},
		httpClient: &http.Client{}, // 超时由 doPost 的 context deadline 控制

		// 容错默认参数：退避基数 1s（1s→2s→4s）、20% 抖动。
		// 注意：启动入口按秒配置超时，退避基数默认 1s；测试可覆盖为更小值以快速自测。
		retryBase:   time.Second,
		jitterRatio: 0.2,
	}
}

// ============ Chat 对话 ============

// openaiChatReq 发给 /chat/completions 的开放格式请求体
type openaiChatReq struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
}

// openaiChatResp 解析 /chat/completions 返回体
type openaiChatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// Chat 实现对话接口
func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := openaiChatReq{
		Model:       c.cfg.ChatModel,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}

	// 组装 JSON
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 发起 POST 请求（对话使用主配置的 baseURL + apiKey）
	url := c.cfg.BaseURL + "/chat/completions"
	respBytes, err := c.doPost(ctx, url, c.cfg.APIKey, payload)
	if err != nil {
		return nil, err
	}

	// 解析响应
	var resp openaiChatResp
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("响应中没有 choices 内容")
	}

	return &ChatResponse{
		Content: resp.Choices[0].Message.Content,
		Usage: TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}, nil
}

// ============ Embedding 向量生成 ============

// openaiEmbedReq 发给 /embeddings 的开放格式请求体
type openaiEmbedReq struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// openaiEmbedResp 解析 /embeddings 返回体
type openaiEmbedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed 实现向量生成接口
func (c *OpenAIClient) Embed(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error) {
	body := openaiEmbedReq{
		Model: c.cfg.EmbeddingModel,
		Input: req.Input,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 向量服务可独立配置：优先用 EmbedBaseURL/EmbedAPIKey，为空则回退用主配置
	baseURL, apiKey := c.embedEndpoint()
	url := baseURL + "/embeddings"
	respBytes, err := c.doPost(ctx, url, apiKey, payload)
	if err != nil {
		return nil, err
	}

	var resp openaiEmbedResp
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("响应中没有向量数据")
	}

	return &EmbeddingResponse{
		Vector: resp.Data[0].Embedding,
		Usage: TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: 0,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}, nil
}

// embedEndpoint 返回向量服务使用的 BaseURL 与 APIKey。
// 若配置了独立的 EmbedBaseURL/EmbedAPIKey 则用之（实现多厂商），否则回退用主配置（单厂商）。
func (c *OpenAIClient) embedEndpoint() (baseURL, apiKey string) {
	baseURL = c.cfg.EmbedBaseURL
	apiKey = c.cfg.EmbedAPIKey
	if baseURL == "" {
		baseURL = c.cfg.BaseURL
	}
	if apiKey == "" {
		apiKey = c.cfg.APIKey
	}
	return baseURL, apiKey
}

// ============ 容错封装：错误分类 + 指数退避重试 ============

// APIError 表示 LLM 接口返回的 HTTP 层错误，携带是否可重试的标记，
// 供通用重试包装器 `withRetry` 决策。
type APIError struct {
	StatusCode int    // HTTP 状态码
	Body       string // 响应体片段（便于排障）
	Retryable  bool   // 是否可重试
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// retryableErr 判断一个错误是否为可重试错误。
// 可重试：网络错误（连接失败/超时）对应的 *APIError.Retryable==true、HTTP 5xx。
// 注意：HTTP 429 限流当前返回 Retryable=false（方案A），但理论上是"可延迟重试"，
// 后续可单独用 Retry-After / 慢退避接入（见 README 注意点）。
func retryableErr(err error) bool {
	if err == nil {
		return false
	}
	ae, ok := err.(*APIError)
	if !ok {
		// 非 APIError（如 io 读取失败）视为可重试的瞬时错误
		return true
	}
	return ae.Retryable
}

// withRetry 通用指数退避重试包装器（业务/客户端通用，后续熔断也可复用）。
//
// 参数：
//   - fn:            一次真正的尝试。返回 nil 即成功；返回错误交由重试策略决定。
//   - isRetryable:   判断某个错误是否可重试（不可重试则立即返回，不再退避）。
//   - maxRetries:    最多重试次数（N 次重试 = 最多发起 N+1 次尝试）。
//   - baseDelay:     退避基数，每隔 N 次重试前等待 baseDelay * 2^(N-1)，即 1s→2s→4s。
//   - jitterRatio:   抖动比例（0~1），在退避间隔上叠加随机量，避免多客户端同时重试造成雪崩。
//
// 返回最后一次尝试的错误（重试耗尽时）；若中止/取消则返回 ctx 相关错误。
func withRetry(ctx context.Context, fn func(attempt int) error, isRetryable func(err error) bool,
	maxRetries int, baseDelay time.Duration, jitterRatio float64) error {

	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// 上下文被取消时直接中断，不再发起新尝试
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("请求中断: %w", err)
		}

		lastErr = fn(attempt)

		if lastErr == nil {
			return nil // 成功
		}

		// 不可重试（调用方错误 / 限流策略 / 已到重试上限）→ 直接返回，不再退避
		if !isRetryable(lastErr) || attempt >= maxRetries {
			return lastErr
		}

		// 计算指数退避：1s → 2s → 4s（基数 baseDelay）
		// 抖动：在 [1, 1+jitterRatio] 放大退避，随机错开并发重试
		backoff := baseDelay * time.Duration(1<<uint(attempt))
		jitter := 1 + (rand.Float64() * jitterRatio)
		delay := time.Duration(float64(backoff) * jitter)

		select {
		case <-ctx.Done():
			return fmt.Errorf("请求中断: %w", ctx.Err())
		case <-time.After(delay):
		}
	}
	return lastErr
}

// ============ HTTP 公共封装 ============

// doPost 发起带鉴权头的 POST 请求，返回响应体字节。
// apiKey 可单独传入（对话/向量可各自使用不同厂商的 key）。
//
// 容错策略叠加：
//  1. 超时控制（整体 deadline 预算）：若外部 ctx 未设截止时间，则为整个请求周期
//     （含所有重试）创建基于配置 Timeout 的 context，总耗时不超过它，防死循环。
//  2. 指数退避重试：1s→2s→4s + 20% 抖动，只对可重试错误重试。
//  3. 可重试：网络错误、超时、HTTP 5xx。
//  4. 不重试：HTTP 401/400/429 等（429 见 README 注意点，后续单独接入慢退避）。
func (c *OpenAIClient) doPost(ctx context.Context, url, apiKey string, payload []byte) ([]byte, error) {
	// 若外部 ctx 未设截止时间，则基于配置的超时创建整体 deadline。
	// 整个重试循环共享这个 ctx：时间预算耗尽即快速失败。
	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}

	var out []byte

	err := withRetry(reqCtx,
		func(attempt int) error {
			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
			if err != nil {
				return fmt.Errorf("构造请求失败: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+apiKey) // 鉴权头

			resp, err := c.httpClient.Do(req)
			if err != nil {
				// 网络层错误（连接失败、超时等）→ 可重试
				return &APIError{StatusCode: 0, Body: err.Error(), Retryable: true}
			}
			defer resp.Body.Close()

			respBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return &APIError{StatusCode: resp.StatusCode, Body: "读取响应失败: " + err.Error(), Retryable: true}
			}

			// 2xx 成功
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				out = respBytes
				return nil
			}

			// 非 2xx：根据状态码标记是否可重试
			//  - 5xx 服务器错误 → 可重试
			//  - 429 限流 → 当前不重试（方案A；后续可接入 Retry-After 慢退避）
			//  - 其余 4xx（401/400/404 等）→ 调用方错误，不重试
			retryable := resp.StatusCode >= 500
			return &APIError{
				StatusCode: resp.StatusCode,
				Body:       string(respBytes),
				Retryable:  retryable,
			}
		},
		retryableErr,
		c.cfg.MaxRetries,
		c.retryBase,
		c.jitterRatio,
	)
	if err != nil {
		return nil, err
	}
	return out, nil
}
