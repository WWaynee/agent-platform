package llmclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"agent-platform/config"
	"agent-platform/observability"

	"go.uber.org/zap"
)

// ============ 面向接口设计 ============

// Client 是 LLM 客户端接口。
// ⚠️ 核心原则：业务层（agent / RAG / service）只依赖这个接口，
// 不依赖具体厂商实现。以后换模型厂商（OpenAI/DeepSeek/通义...），
// 只需新增一个实现类型，业务层代码零改动。
type Client interface {
	// Chat 发送对话请求，返回助手回复
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// ChatWithJSON 发送对话并要求返回严格 JSON，解析结果写入 target
	// （面向 Agent 结构化调用：ReAct 的 Action 解析等；含格式修复与一次重试）
	ChatWithJSON(ctx context.Context, req ChatRequest, target interface{}) error
	// Embed 将文本转为向量（用于 RAG 检索）
	Embed(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error)
	// EmbedBatch 批量将多条文本转为向量，一次请求返回所有向量（RAG 切片向量化用）
	EmbedBatch(ctx context.Context, req EmbeddingBatchRequest) (*EmbeddingBatchResponse, error)
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

	// cb 简易熔断器：Closed →（失败率超阈值）→ Open →（超时后）→ Half-Open →（试探）→ 回 Closed/Open。
	cb *CircuitBreaker

	// 用量统计：内置累计统计 + 可注入回调钩子（供租户用量统计/限流，见 usage.go）。
	usageMu       sync.Mutex    // 保护 usageReporter
	usageReporter UsageReporter // 用量回调钩子（可空）
	usageStats    *UsageStats   // 内置累计用量统计（始终启用）
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

		// 熔断器：窗口 10s / 最少 5 次请求评估 / 失败率 50% 触发 / 熔断持续 30s
		cb: newCircuitBreaker(CircuitBreakerConfig{
			FailureThreshold: 0.5,
			MinRequests:      5,
			Window:           10 * time.Second,
			OpenTimeout:      30 * time.Second,
		}),

		usageStats: &UsageStats{},
	}
}

// SetUsageReporter 注册用量回调钩子。
// 上层可用它做租户用量统计、持久化、限流决策等；传 nil 表示取消回调。
// 支持并发调用，注册后对之后的调用生效。
func (c *OpenAIClient) SetUsageReporter(r UsageReporter) {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	c.usageReporter = r
}

// GetUsageStats 返回内置累计用量统计的快照（共调用次数、prompt/completion/total 总量）。
// 不加回调也能拿到客户端累计消耗的整体视图。
func (c *OpenAIClient) GetUsageStats() (calls int, prompt, completion, total int64) {
	return c.usageStats.Snapshot()
}

// reportUsage 在每次调用完成后触发：① 累加进内置统计；② 报告给回调钩子；③ 打 LLM 调用日志。
// ctx 用发起本次调用的上下文，使上层能通过 WithValue 读取租户等标识。
func (c *OpenAIClient) reportUsage(ctx context.Context, ev UsageEvent) {
	// 无论成功失败都累加计数（失败无 token 消耗，总量不会虚增）
	c.usageStats.add(ev.Operation, ev.Tokens)

	// Prometheus 指标埋点（LLM 计数是该客户端的统一必经点，覆盖 Chat/Embed/EmbedBatch）：
	//  - llm_calls_total +1（标签 model / success）
	//  - llm_tokens_total 累计 token（标签 model）
	// 标签用低基数 model / success（不用 trace_id/user_id，防基数爆炸）。
	observability.IncLLMCall(ev.Model, ev.Success)
	if ev.Tokens.TotalTokens > 0 {
		observability.AddLLMTokens(ev.Model, float64(ev.Tokens.TotalTokens))
	}

	// ① LLM 调用日志（关键链路）：记录 model / 时长 / token 用量 / 是否成功 / 错误信息。
	//    经 WithContext 自动带上 trace_id / tenant_id（若 ctx 携带）。
	logger := observability.WithContext(ctx)
	fields := []zap.Field{
		zap.String("model", ev.Model),
		zap.Int64(observability.FieldLatency, ev.Duration.Milliseconds()),
		zap.Int("prompt_tokens", ev.Tokens.PromptTokens),
		zap.Int("completion_tokens", ev.Tokens.CompletionTokens),
		zap.Int("total_tokens", ev.Tokens.TotalTokens),
		zap.Bool("ok", ev.Success),
	}
	if ev.Success {
		logger.Info("LLM 调用成功", fields...)
	} else {
		logger.Warn("LLM 调用失败",
			append(fields, zap.Error(ev.Error))...,
		)
	}

	// ② 回调钩子（可空）；取引用时加锁，避免与 SetUsageReporter 并发覆盖
	c.usageMu.Lock()
	rep := c.usageReporter
	c.usageMu.Unlock()
	if rep != nil {
		rep.Report(ctx, ev)
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
	start := time.Now()
	respBytes, err := c.doPost(ctx, url, c.cfg.APIKey, payload)
	if err != nil {
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationChat, Model: c.cfg.ChatModel,
			Success: false, Duration: time.Since(start), Error: err,
		})
		return nil, err
	}

	// 解析响应
	var resp openaiChatResp
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		perr := fmt.Errorf("解析响应失败: %w", err)
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationChat, Model: c.cfg.ChatModel,
			Success: false, Duration: time.Since(start), Error: perr,
		})
		return nil, perr
	}
	if len(resp.Choices) == 0 {
		perr := fmt.Errorf("响应中没有 choices 内容")
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationChat, Model: c.cfg.ChatModel,
			Success: false, Duration: time.Since(start), Error: perr,
		})
		return nil, perr
	}

	usage := TokenUsage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	c.reportUsage(ctx, UsageEvent{
		Ctx: ctx, Operation: OperationChat, Model: c.cfg.ChatModel,
		Tokens: usage, Success: true, Duration: time.Since(start),
	})
	return &ChatResponse{Content: resp.Choices[0].Message.Content, Usage: usage}, nil
}

// ============ ChatWithJSON：结构化 JSON 输出校验与修复 ============

// JSONFormatError 表示 LLM 返回内容在"简单修复 + 重试一次"后仍无法解析为合法 JSON。
type JSONFormatError struct {
	Raw string // 模型最后一次原始返回，便于排障
}

func (e *JSONFormatError) Error() string {
	return fmt.Sprintf("LLM 返回内容无法解析为合法 JSON（重试后仍失败）: %s", e.Raw)
}

// jsonSystemConstraint 注入的 system 提示，明确要求严格 JSON 输出。
const jsonSystemConstraint = "你必须只返回严格的合法 JSON 对象，不要包含任何解释文字、" +
	"不要使用 Markdown 代码块标记（如 ```json```）,不要加前后缀。返回内容必须能被直接解析为 JSON。"

// injectJSONSystemPrompt 在请求最前面注入"严格 JSON"system 提示（不污染调用方原请求）。
func injectJSONSystemPrompt(req ChatRequest) ChatRequest {
	systemMsg := ChatMessage{Role: RoleSystem, Content: jsonSystemConstraint}
	req.Messages = append([]ChatMessage{systemMsg}, req.Messages...)
	return req
}

// addRepairHint 在重试时追加一条提示，明确告知模型上次返回的不是合法 JSON。
func addRepairHint(req ChatRequest) ChatRequest {
	req.Messages = append(req.Messages, ChatMessage{
		Role:    RoleSystem,
		Content: "你上一次返回的不是合法 JSON。请重新只返回一个合法 JSON 对象，不要加任何其他文字。",
	})
	return req
}

// ChatWithJSON 发送对话并要求返回严格 JSON，解析结果写入 target。
// 面向 Agent/结构化调用场景（ReAct 的 Action 解析等）：
//  1. 注入 system 提示明确要求模型返回严格 JSON。
//  2. 拿到响应后先尝试直接解析；失败则做【简单修复】（去 ```json 包裹 / 去前后文字 / 补括号）。
//  3. 修复后仍无法解析 → 重试一次，明确告知模型"只返回 JSON"。
//  4. 重试后仍失败 → 返回 *JSONFormatError（含原始内容便于排障）。
//
// 返回错误时 target 内容不可靠；返回 nil 表示 target 已被正确填充。
func (c *OpenAIClient) ChatWithJSON(ctx context.Context, req ChatRequest, target interface{}) error {
	req = injectJSONSystemPrompt(req)

	if err := c.callAndRepair(ctx, req, target); err == nil {
		return nil
	} else if !isJSONFormatError(err) {
		// 上游错误（网络/超时/熔断等）不是格式问题，直接返回，不触发"重试改格式"
		return err
	}

	// 格式错误 → 重试一次，明确告知模型只返回 JSON
	retryReq := addRepairHint(req)
	if err := c.callAndRepair(ctx, retryReq, target); err != nil {
		return err
	}
	return nil
}

// callAndRepair 单次"调 Chat → 简单修复 → 解析进 target"。
// 返回 nil 表示解析成功；返回 *JSONFormatError 表示修复后仍无法解析。
func (c *OpenAIClient) callAndRepair(ctx context.Context, req ChatRequest, target interface{}) error {
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return err
	}
	content := resp.Content

	// 先直接尝试解析
	if err := json.Unmarshal([]byte(content), target); err == nil {
		return nil
	}

	// 直接解析失败 → 简单修复后再试
	fixed := normalizeJSON(content)
	if err := json.Unmarshal([]byte(fixed), target); err != nil {
		return &JSONFormatError{Raw: content}
	}
	return nil
}

// isJSONFormatError 判断 err 是否为格式错误（仅格式错误才触发"重试改格式"）。
func isJSONFormatError(err error) bool {
	var jerr *JSONFormatError
	return errors.As(err, &jerr)
}

// normalizeJSON 尝试把 LLM 返回的多余文字/包裹还原为一段可解析的 JSON。
// 依次执行：
//  1. 去掉 ```json ... ``` 代码块包裹；
//  2. 截取首个 "{" 到最后一个 "}" 之间的内容（丢弃前后多余文字）；
//  3. 若 "{" 比 "}" 多，补上缺少的 "}"（简单补括号）；
//  4. 去掉首尾空白。
func normalizeJSON(raw string) string {
	s := strings.TrimSpace(raw)

	// 1. 去掉 ```json 代码块包裹
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		} else {
			s = strings.TrimPrefix(s, "```")
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}

	// 2. 截取首个 { 到最后一个 } 之间的对象骨架
	first := strings.Index(s, "{")
	last := strings.LastIndex(s, "}")
	if first != -1 && last != -1 && last > first {
		s = s[first : last+1]
	}

	// 3. 简单补括号
	opens := strings.Count(s, "{")
	closes := strings.Count(s, "}")
	if opens > closes {
		s += strings.Repeat("}", opens-closes)
	}

	// 4. 去首尾空白
	return strings.TrimSpace(s)
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
	model := c.cfg.EmbeddingModel
	url := baseURL + "/embeddings"
	start := time.Now()
	respBytes, err := c.doPost(ctx, url, apiKey, payload)
	if err != nil {
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationEmbed, Model: model,
			Success: false, Duration: time.Since(start), Error: err,
		})
		return nil, err
	}

	var resp openaiEmbedResp
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		perr := fmt.Errorf("解析响应失败: %w", err)
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationEmbed, Model: model,
			Success: false, Duration: time.Since(start), Error: perr,
		})
		return nil, perr
	}
	if len(resp.Data) == 0 {
		perr := fmt.Errorf("响应中没有向量数据")
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationEmbed, Model: model,
			Success: false, Duration: time.Since(start), Error: perr,
		})
		return nil, perr
	}

	usage := TokenUsage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: 0,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	c.reportUsage(ctx, UsageEvent{
		Ctx: ctx, Operation: OperationEmbed, Model: model,
		Tokens: usage, Success: true, Duration: time.Since(start),
	})
	return &EmbeddingResponse{Vector: resp.Data[0].Embedding, Usage: usage}, nil
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

// openaiEmbedBatchReq 批量向量请求体（input 传数组，一次请求多条）
type openaiEmbedBatchReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbedBatch 批量向量生成：一次请求为多条文本生成向量。
// 返回的 Vectors 与 req.Inputs 一一对应。
//
// OpenAI 兼容 /embeddings 接口支持 input 传字符串数组，一次返回所有 embedding。
// 文档切片向量化场景用它替代逐条调用，显著减少 HTTP 往返与耗时。
func (c *OpenAIClient) EmbedBatch(ctx context.Context, req EmbeddingBatchRequest) (*EmbeddingBatchResponse, error) {
	if len(req.Inputs) == 0 {
		return &EmbeddingBatchResponse{Vectors: [][]float32{}, Usage: TokenUsage{}}, nil
	}

	body := openaiEmbedBatchReq{
		Model: c.cfg.EmbeddingModel,
		Input: req.Inputs,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	baseURL, apiKey := c.embedEndpoint()
	model := c.cfg.EmbeddingModel
	url := baseURL + "/embeddings"
	start := time.Now()
	respBytes, err := c.doPost(ctx, url, apiKey, payload)
	if err != nil {
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationEmbed, Model: model,
			Success: false, Duration: time.Since(start), Error: err,
		})
		return nil, err
	}

	// 复用单条响应的解析结构（Data[].Embedding）
	var resp openaiEmbedResp
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		perr := fmt.Errorf("解析响应失败: %w", err)
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationEmbed, Model: model,
			Success: false, Duration: time.Since(start), Error: perr,
		})
		return nil, perr
	}
	if len(resp.Data) != len(req.Inputs) {
		perr := fmt.Errorf("批量向量数量不符: 期望 %d 条，实际返回 %d 条", len(req.Inputs), len(resp.Data))
		c.reportUsage(ctx, UsageEvent{
			Ctx: ctx, Operation: OperationEmbed, Model: model,
			Success: false, Duration: time.Since(start), Error: perr,
		})
		return nil, perr
	}

	vectors := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		vectors[i] = d.Embedding
	}

	usage := TokenUsage{
		PromptTokens: resp.Usage.PromptTokens,
		TotalTokens:  resp.Usage.TotalTokens,
	}
	c.reportUsage(ctx, UsageEvent{
		Ctx: ctx, Operation: OperationEmbed, Model: model,
		Tokens: usage, Success: true, Duration: time.Since(start),
	})
	return &EmbeddingBatchResponse{Vectors: vectors, Usage: usage}, nil
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
	// 熔断错误：熔断期间不发请求、也不重试（避免打垮已故障的下游）
	if errors.Is(err, ErrCircuitOpen) {
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

// ============ 简易熔断器（Closed / Open / Half-Open） ============

// ErrCircuitOpen 表示熔断器处于 Open 状态，请求被快速拒绝，未发起 HTTP 请求。
var ErrCircuitOpen = fmt.Errorf("熔断器已打开（circuit open）: LLM 服务疑似不可用，已快速失败")

// CircuitBreakerState 熔断器状态
type CircuitBreakerState int

const (
	// StateClosed 关闭：正常状态，请求全部放行
	StateClosed CircuitBreakerState = iota
	// StateOpen 打开：所有请求直接拒绝，不发 HTTP
	StateOpen
	// StateHalfOpen 半开：放少量试探请求，探测服务是否恢复
	StateHalfOpen
)

func (s CircuitBreakerState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	}
	return "unknown"
}

// CircuitBreakerConfig 熔断器参数配置。默认值见 NewCircuitBreaker 内。
type CircuitBreakerConfig struct {
	FailureThreshold float64       // 失败率阈值（0~1），超过则打开熔断，如 0.5
	MinRequests      int           // 触发评估的最少请求数，低于此值不评估（防冷启动误判）
	Window           time.Duration // 统计时间窗口，如 10s
	OpenTimeout      time.Duration // 熔断持续时长，超过后进入半开试探，如 30s
}

// CircuitBreaker 简易熔断器：用"简单计数器 + 时间窗口"实现，无滑动窗口。
// 并发安全：所有方法加互斥锁。
type CircuitBreaker struct {
	failureThreshold float64
	minRequests      int
	window           time.Duration
	openTimeout      time.Duration

	mu sync.Mutex

	state        CircuitBreakerState
	requestCount int // 窗口内请求总数
	failureCount int // 窗口内失败数
	windowStart  time.Time

	openSince         time.Time // Open 起始时刻（用于切换到 Half-Open）
	halfOpenProbeSent bool      // Half-Open 下是否已放过那个试探请求

	nowFunc func() time.Time // 时钟函数（测试可注入假时钟）
}

// NewCircuitBreaker 构造熔断器，未配置的字段使用默认值。
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:   StateClosed,
		nowFunc: time.Now,
	}
	if cfg.FailureThreshold > 0 {
		cb.failureThreshold = cfg.FailureThreshold
	} else {
		cb.failureThreshold = 0.5
	}
	if cfg.MinRequests > 0 {
		cb.minRequests = cfg.MinRequests
	} else {
		cb.minRequests = 5
	}
	if cfg.Window > 0 {
		cb.window = cfg.Window
	} else {
		cb.window = 10 * time.Second
	}
	if cfg.OpenTimeout > 0 {
		cb.openTimeout = cfg.OpenTimeout
	} else {
		cb.openTimeout = 30 * time.Second
	}
	cb.windowStart = cb.nowFunc()
	return cb
}

// State 返回当前状态（供测试/观察）。
func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	// 先推进窗口/半开切换，保证返回的是最新状态
	cb.advance()
	return cb.state
}

// allow 在发起请求前调用。返回 true 表示放行请求。
// Closed 全放行；Open 全拒绝；Half-Open 只放第一个试探请求。
func (cb *CircuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.advance()

	switch cb.state {
	case StateClosed, StateHalfOpen:
		// Closed：全放行；Half-Open：只放第一个试探请求
		if cb.state == StateHalfOpen {
			if cb.halfOpenProbeSent {
				return false // 半开下已放过试探，其余拒绝
			}
			cb.halfOpenProbeSent = true
		}
		return true
	case StateOpen:
	default:
	}
	return false // Open：直接拒绝
}

// record 在请求完成后调用，记录一次成功/失败，并据此流转状态。
func (cb *CircuitBreaker) record(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := cb.nowFunc()
	cb.advance()

	switch cb.state {
	case StateHalfOpen:
		// 试探请求的结果决定下一步
		if success {
			cb.resetCounters(now)
			cb.state = StateClosed
		} else {
			cb.openSince = now
			cb.state = StateOpen
		}
		return

	case StateClosed:
		if success {
			cb.requestCount++
		} else {
			cb.requestCount++
			cb.failureCount++
		}
		// 请求数足够且失败率超阈值 → 打开熔断
		if cb.requestCount >= cb.minRequests &&
			float64(cb.failureCount)/float64(cb.requestCount) > cb.failureThreshold {
			cb.openSince = now
			cb.state = StateOpen
		}

	case StateOpen:
		// Open 下请求被拒绝，不记录（advance 已处理切换到半开的逻辑）
	}
}

// advance 推进内部状态：处理窗口到期重置、Open→Half-Open 的时间切换。
func (cb *CircuitBreaker) advance() {
	now := cb.nowFunc()
	switch cb.state {
	case StateClosed:
		// 窗口到期 → 重置统计（简单时间窗口，非滑动窗口）
		if now.Sub(cb.windowStart) > cb.window {
			cb.resetCounters(now)
		}
	case StateOpen:
		// 熔断持续超时 → 进入半开，准备放试探请求
		if now.Sub(cb.openSince) >= cb.openTimeout {
			cb.state = StateHalfOpen
			cb.halfOpenProbeSent = false
		}
	}
}

// resetCounters 清零窗口内统计并把窗口起始推进到 now。
func (cb *CircuitBreaker) resetCounters(now time.Time) {
	cb.requestCount = 0
	cb.failureCount = 0
	cb.windowStart = now
}

// newCircuitBreaker 便捷构造（与 NewCircuitBreaker 等价，内部用于默认实例）。
func newCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	return NewCircuitBreaker(cfg)
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
			// 熔断判决：Open 直接拒绝不发请求；Half-Open 只放试探请求
			if !c.cb.allow() {
				return ErrCircuitOpen
			}

			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
			if err != nil {
				return fmt.Errorf("构造请求失败: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+apiKey) // 鉴权头

			resp, err := c.httpClient.Do(req)
			if err != nil {
				// 网络层错误（连接失败、超时等）→ 可重试；记入熔断失败
				c.cb.record(false)
				return &APIError{StatusCode: 0, Body: err.Error(), Retryable: true}
			}
			defer resp.Body.Close()

			respBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				c.cb.record(false)
				return &APIError{StatusCode: resp.StatusCode, Body: "读取响应失败: " + err.Error(), Retryable: true}
			}

			// 2xx 成功
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				c.cb.record(true)
				out = respBytes
				return nil
			}

			// 非 2xx：根据状态码标记是否可重试
			//  - 5xx 服务器错误 → 可重试
			//  - 429 限流 → 当前不重试（方案A；后续可接入 Retry-After 慢退避）
			//  - 其余 4xx（401/400/404 等）→ 调用方错误，不重试
			retryable := resp.StatusCode >= 500
			c.cb.record(false)
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
