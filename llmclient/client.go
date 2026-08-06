package llmclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// ============ HTTP 公共封装 ============

// doPost 发起带鉴权头的 POST 请求，返回响应体字节。
// apiKey 可单独传入（对话/向量可各自使用不同厂商的 key）。
//
// 容错策略（方案 B：超时 + 重试 + 整体 deadline 预算）：
//  1. 整个请求过程（含所有重试）共享一个 deadline，总耗时不超过配置的 Timeout，
//     deadline 到达后立即停止，即使重试次数未耗尽也不会再发起新请求（防死循环）。
//  2. 可重试的错误：网络错误（连接失败/超时）、HTTP 429 限流、HTTP 5xx 服务端错误。
//  3. 不可重试的错误：4xx（如 401 鉴权失败）为调用方错误，直接返回不重试。
//  4. 重试间隙的退避等待会响应 ctx 取消 / deadline，到了立即中断。
func (c *OpenAIClient) doPost(ctx context.Context, url, apiKey string, payload []byte) ([]byte, error) {
	// 若外部 ctx 未设截止时间，则基于配置的超时创建整体 deadline。
	// 整个重试循环共享这个 ctx：时间预算耗尽即快速失败。
	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}

	var lastErr error

	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		// 退避等待 + 检查 deadline/取消（select 保证可被 ctx 打断）
		if attempt > 0 {
			delay := time.Duration(1<<uint(attempt-1)) * 250 * time.Millisecond
			select {
			case <-reqCtx.Done():
				// 总预算已耗尽，快速失败，不再重试
				return nil, wrapDeadlineErr(reqCtx, lastErr)
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("构造请求失败: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey) // 鉴权头

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// 网络层错误（连接失败、超时等），可重试；若已到总预算则快速失败
			lastErr = fmt.Errorf("请求失败: %w", err)
			if reqCtx.Err() != nil {
				return nil, wrapDeadlineErr(reqCtx, lastErr)
			}
			continue
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("读取响应失败: %w", err)
			continue
		}

		// 2xx 直接返回
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return respBytes, nil
		}

		// 429 / 5xx 可重试；4xx（如 401）为调用方错误，不重试
		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBytes))
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

// wrapDeadlineErr 当总预算耗尽或外部 ctx 被取消时，返回清晰的错误信息。
func wrapDeadlineErr(ctx context.Context, lastErr error) error {
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("请求超时（超过配置的截止时间）: %v", lastErr)
	}
	return ctx.Err()
}
