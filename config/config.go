package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// MySQL 配置
type MySQLConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

// Redis 配置
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int // Redis 逻辑库号：会话记忆/限流等可按需分库（默认 0）
}

// MinIO 配置
type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool // 本地开发 false，线上走 HTTPS 为 true
}

// OSSConfig 阿里云对象存储（OSS）配置（需求单 0010：MinIO → 阿里云 OSS 迁移）。
type OSSConfig struct {
	Region      string // 地域，如 cn-shenzhen
	Endpoint    string // 公网 endpoint，如 oss-cn-shenzhen.aliyuncs.com
	AccessKeyID string // AccessKey ID
	AccessKeySecret string // AccessKey Secret
	Bucket      string // 存储桶名，如 my-agent-file
}

// Qdrant 配置
type QdrantConfig struct {
	Host string
	// Port REST API 端口（6333）；本项目当前 SDK 走 gRPC(GRPCPort)，此字段保留便于查看
	Port int
	// GRPCPort gRPC 端口（通过 QDRANT_GRPC_PORT 配置），Qdrant Go SDK 使用该端口连接。
	// Qdrant：REST=6333，gRPC=6334
	GRPCPort int
	// CollectionName 向量集合名（如 documents），写入/检索都针对该集合
	CollectionName string
}

// JWT 配置
type JWTConfig struct {
	Secret        string // 签名密钥
	ExpireSeconds int64  // 过期时间（秒）
}

// LLM 配置
type LLMConfig struct {
	APIKey         string // API Key（OpenAI 兼容接口，对话用）
	BaseURL        string // 基础地址（对话用）
	ChatModel      string // 对话模型名
	EmbeddingModel string // 向量模型名

	// 独立向量服务配置（可选）。
	// 若为空则自动回退用上方 APIKey/BaseURL（即对话、向量同厂商）。
	// 设置后可让 Chat 与 Embedding 使用不同厂商（如 DeepSeek 对话 + 硅基流动向量）。
	EmbedAPIKey  string // 向量服务 API Key
	EmbedBaseURL string // 向量服务基础地址

	Timeout    int // 请求超时时间（秒）
	MaxRetries int // 最大重试次数
}

// RabbitMQ 配置
type RabbitMQConfig struct {
	Host     string // AMQP 主机地址
	Port     int    // AMQP 端口（默认 5672）
	Username string // 用户名
	Password string // 密码
	Vhost    string // 虚拟主机（默认 /）
	// QueueName 文档解析队列名（异步任务投递/消费统一用该队列）
	QueueName string
}

// 服务配置
type ServerConfig struct {
	HTTPPort int // HTTP 业务监听端口（公网）
	// MetricsPort Prometheus 指标监听端口（内网专用，不与公网业务端口混用）。
	// 设为 0 表示不启动独立 metrics 服务（禁用指标暴露）。
	MetricsPort int
}

// LogConfig 日志配置
type LogConfig struct {
	// Level 日志级别：debug（开发，最详细）/ info（生产，默认）。生产环境用 info，开发用 debug。
	Level string
	// File 日志文件路径。为空则只输出到 stdout；设置后同时写文件（便于落盘归档 / 采集）
	File string
}

// RateLimitConfig 限流配置（滑动窗口，Redis 分布式实现）
type RateLimitConfig struct {
	TenantPerMin int // 每个租户每分钟最大请求数（所有私有接口合计），默认 300
	UserPerMin   int // 每个用户每分钟最大请求数（单个用户），默认 60
	ChatPerMin   int // 对话接口单独更严格地限流（调 LLM 成本高），默认 20
	WindowSec    int // 滑动窗口时长（秒），默认 60
	// KeyTTL 限流 key 过期时间（秒），默认比窗口大一点（如 window*2），用于自动清理
	KeyTTL int
}

// QuotaConfig 租户配额配置（token 配额，须在配额拦截处使用）
type QuotaConfig struct {
	// DefaultMonthlyToken 新租户默认的每月 token 配额（0 表示不限制）
	DefaultMonthlyToken int64
}

// UsageConfig 用量统计配置（Redis 实时按天计数，不做 MySQL 持久化）
type UsageConfig struct {
	// RedisTTL 用量 key 过期天数（保留最近 N 天历史），默认 30
	RedisTTL int
}

// Config 全局配置根结构体
type Config struct {
	MySQL     MySQLConfig
	Redis     RedisConfig
	MinIO     MinIOConfig
	OSS       OSSConfig
	Qdrant    QdrantConfig
	JWT       JWTConfig
	LLM       LLMConfig
	RabbitMQ  RabbitMQConfig
	Server    ServerConfig
	RateLimit RateLimitConfig
	Quota     QuotaConfig
	Usage     UsageConfig
	Log       LogConfig
}

// GlobalConfig 全局配置实例，程序启动时加载一次
var GlobalConfig Config

// Load 加载 .env 文件并解析配置到 GlobalConfig
func Load() error {
	// 1. 加载 .env 文件（忽略已存在系统环境变量的情况）
	_ = godotenv.Load(".env")

	// 2. 读取环境变量并赋值
	cfg := Config{}

	// MySQL
	cfg.MySQL = MySQLConfig{
		Host:     getEnv("MYSQL_HOST", "127.0.0.1"),
		Port:     getEnvInt("MYSQL_PORT", 3306),
		User:     getEnv("MYSQL_USER", "root"),
		Password: getEnv("MYSQL_ROOT_PWD", ""),
		DBName:   getEnv("MYSQL_DB", ""),
	}

	// Redis
	cfg.Redis = RedisConfig{
		Host:     getEnv("REDIS_HOST", "127.0.0.1"),
		Port:     getEnvInt("REDIS_PORT", 6379),
		Password: getEnv("REDIS_PASSWORD", ""),
		DB:       getEnvInt("REDIS_DB", 0),
	}

	// MinIO
	cfg.MinIO = MinIOConfig{
		Endpoint:  getEnv("MINIO_ENDPOINT", fmt.Sprintf("127.0.0.1:%d", getEnvInt("MINIO_PORT", 9000))),
		AccessKey: getEnv("MINIO_ACCESS_KEY", "admin"),
		SecretKey: getEnv("MINIO_SECRET_KEY", ""),
		Bucket:    getEnv("MINIO_BUCKET", "document-store"),
		UseSSL:    getEnvBool("MINIO_USE_SSL", false),
	}

	// OSS（阿里云对象存储，需求单 0010）
	cfg.OSS = OSSConfig{
		Region:          getEnv("OSS_REGION", "cn-hangzhou"),
		Endpoint:        getEnv("OSS_ENDPOINT", "oss-cn-hangzhou.aliyuncs.com"),
		AccessKeyID:     getEnv("OSS_ACCESS_KEY_ID", ""),
		AccessKeySecret: getEnv("OSS_ACCESS_KEY_SECRET", ""),
		Bucket:          getEnv("OSS_BUCKET", "document-store"),
	}

	// Qdrant
	cfg.Qdrant = QdrantConfig{
		Host:           getEnv("QDRANT_HOST", "127.0.0.1"),
		Port:           getEnvInt("QDRANT_PORT", 6333),
		GRPCPort:       getEnvInt("QDRANT_GRPC_PORT", 6334),
		CollectionName: getEnv("QDRANT_COLLECTION", "documents"),
	}

	// JWT（默认 24 小时）
	cfg.JWT = JWTConfig{
		Secret:        getEnv("JWT_SECRET", "agent-platform-secret"),
		ExpireSeconds: getEnvInt64("JWT_EXPIRE_SECONDS", 24*60*60),
	}

	// LLM
	cfg.LLM = LLMConfig{
		APIKey:         getEnv("LLM_API_KEY", ""),
		BaseURL:        getEnv("LLM_BASE_URL", "https://api.deepseek.com"),
		ChatModel:      getEnv("LLM_CHAT_MODEL", "deepseek-chat"),
		EmbeddingModel: getEnv("LLM_EMBEDDING_MODEL", "text-embedding-v1"),
		// 可选：独立向量服务（不填则回退用上面的 deepseek）
		EmbedAPIKey:  getEnv("LLM_EMBED_API_KEY", ""),
		EmbedBaseURL: getEnv("LLM_EMBED_BASE_URL", ""),
		Timeout:      getEnvInt("LLM_TIMEOUT_SECONDS", 30),
		MaxRetries:   getEnvInt("LLM_MAX_RETRY", 3),
	}

	// RabbitMQ（异步任务：文档解析后台处理）
	cfg.RabbitMQ = RabbitMQConfig{
		Host:      getEnv("RABBITMQ_HOST", "127.0.0.1"),
		Port:      getEnvInt("RABBITMQ_PORT", 5672),
		Username:  getEnv("RABBITMQ_DEFAULT_USER", "guest"),
		Password:  getEnv("RABBITMQ_DEFAULT_PASS", "guest"),
		Vhost:     getEnv("RABBITMQ_VHOST", "/"),
		QueueName: getEnv("RABBITMQ_QUEUE_DOCUMENT_PARSE", "document_parse"),
	}

	// Server
	cfg.Server = ServerConfig{
		HTTPPort:    getEnvInt("SERVER_HTTP_PORT", 8080),
		MetricsPort: getEnvInt("METRICS_PORT", 9090), // 0 = 禁用独立指标端口
	}

	// 限流（滑动窗口，Redis 分布式）：租户/用户/对话三层阈值 + 窗口时长
	cfg.RateLimit = RateLimitConfig{
		TenantPerMin: getEnvInt("RATE_LIMIT_TENANT_PER_MIN", 300),
		UserPerMin:   getEnvInt("RATE_LIMIT_USER_PER_MIN", 60),
		ChatPerMin:   getEnvInt("RATE_LIMIT_CHAT_PER_MIN", 20),
		WindowSec:    getEnvInt("RATE_LIMIT_WINDOW_SEC", 60),
		KeyTTL:       getEnvInt("RATE_LIMIT_KEY_TTL_SEC", 120), // 窗口的 2 倍，用于自动清理
	}

	// 配额：新租户默认每月 token 配额（0 = 不限制）
	cfg.Quota = QuotaConfig{
		DefaultMonthlyToken: getEnvInt64("QUOTA_DEFAULT_MONTHLY_TOKEN", 1000000),
	}

	// 用量统计：Redis key 保留天数（只保留最近 N 天，自动过期清理）
	cfg.Usage = UsageConfig{
		RedisTTL: getEnvInt("USAGE_REDIS_TTL_DAYS", 30),
	}

	// 日志：级别默认 info（生产），开发环境可设 LOG_LEVEL=debug 看最全
	cfg.Log = LogConfig{
		Level: getEnv("LOG_LEVEL", "info"),
		File:  getEnv("LOG_FILE", ""), // 空 = 只输出 stdout
	}

	// 3. 赋值给全局变量
	GlobalConfig = cfg
	return nil
}

// getEnv 读取字符串环境变量，为空时返回默认值
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getEnvInt 读取整型环境变量，转换失败时返回默认值并告警
func getEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		fmt.Printf("[config] 环境变量 %s = %q 不是有效整数，使用默认值 %d\n", key, value, defaultValue)
		return defaultValue
	}
	return n
}

// getEnvInt64 读取 int64 型环境变量
func getEnvInt64(key string, defaultValue int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		fmt.Printf("[config] 环境变量 %s = %q 不是有效整数，使用默认值 %d\n", key, value, defaultValue)
		return defaultValue
	}
	return n
}

// getEnvBool 读取布尔型环境变量
// 支持 true/1/yes/on 视为 true，其余（含空）视为 false
func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		fmt.Printf("[config] 环境变量 %s = %q 不是有效布尔值，使用默认值 %v\n", key, value, defaultValue)
		return defaultValue
	}
	return b
}
