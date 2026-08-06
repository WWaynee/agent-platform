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
	Timeout        time.Duration
	MaxRetries     int
}

// NewClient 构造函数：接收业务层的 llm 配置与全局配置，返回 Client 接口。
// 这里从 config.GlobalConfig.LLM 读取，并实例化带超时的 HTTP 客户端。
func NewClient(llm config.LLMConfig) Client {
	return &OpenAIClient{
		cfg: LLMConfig{
			APIKey:         llm.APIKey,
			BaseURL:        llm.BaseURL,
			ChatModel:      llm.ChatModel,
			EmbeddingModel: llm.EmbeddingModel,
			Timeout:        time.Duration(llm.Timeout) * time.Second,
			MaxRetries:     llm.MaxRetries,
		},
		httpClient: &http.Client{
			Timeout: time.Duration(llm.Timeout) * time.Second,
		},
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

	// 发起 POST 请求
	url := c.cfg.BaseURL + "/chat/completions"
	respBytes, err := c.doPost(ctx, url, payload)
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

	url := c.cfg.BaseURL + "/embeddings"
	respBytes, err := c.doPost(ctx, url, payload)
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

// ============ HTTP 公共封装 ============

// doPost 发起带鉴权头的 POST 请求，返回响应体字节。
// 带回退重试：遇到网络错误/5xx/限流时按退避策略重试。
func (c *OpenAIClient) doPost(ctx context.Context, url string, payload []byte) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// 退避等待 + 检查上下文取消（select 保证可被 ctx 打断）
			delay := time.Duration(1<<uint(attempt-1)) * 250 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("构造请求失败: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey) // 鉴权头

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// 网络层错误（连接失败、超时等），可重试
			lastErr = fmt.Errorf("请求失败: %w", err)
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
