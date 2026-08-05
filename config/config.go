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
}

// MinIO 配置
type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
}

// Qdrant 配置
type QdrantConfig struct {
	Host string
	Port int
}

// JWT 配置
type JWTConfig struct {
	Secret        string // 签名密钥
	ExpireSeconds int64  // 过期时间（秒）
}

// LLM 配置
type LLMConfig struct {
	APIKey   string // API Key
	BaseURL  string // 基础地址
	Model    string // 模型名称
	Timeout  int    // 请求超时时间（秒）
	MaxRetry int    // 最大重试次数
}

// 服务配置
type ServerConfig struct {
	HTTPPort int // HTTP 监听端口
}

// Config 全局配置根结构体
type Config struct {
	MySQL  MySQLConfig
	Redis  RedisConfig
	MinIO  MinIOConfig
	Qdrant QdrantConfig
	JWT    JWTConfig
	LLM    LLMConfig
	Server ServerConfig
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
	}

	// MinIO
	cfg.MinIO = MinIOConfig{
		Endpoint:  getEnv("MINIO_ENDPOINT", fmt.Sprintf("127.0.0.1:%d", getEnvInt("MINIO_PORT", 9000))),
		AccessKey: getEnv("MINIO_ACCESS_KEY", "admin"),
		SecretKey: getEnv("MINIO_SECRET_KEY", ""),
		Bucket:    getEnv("MINIO_BUCKET", "document-store"),
	}

	// Qdrant
	cfg.Qdrant = QdrantConfig{
		Host: getEnv("QDRANT_HOST", "127.0.0.1"),
		Port: getEnvInt("QDRANT_PORT", 6333),
	}

	// JWT（默认 24 小时）
	cfg.JWT = JWTConfig{
		Secret:        getEnv("JWT_SECRET", "agent-platform-secret"),
		ExpireSeconds: getEnvInt64("JWT_EXPIRE_SECONDS", 24*60*60),
	}

	// LLM
	cfg.LLM = LLMConfig{
		APIKey:   getEnv("LLM_API_KEY", ""),
		BaseURL:  getEnv("LLM_BASE_URL", "https://api.deepseek.com"),
		Model:    getEnv("LLM_MODEL", "deepseek-chat"),
		Timeout:  getEnvInt("LLM_TIMEOUT_SECONDS", 30),
		MaxRetry: getEnvInt("LLM_MAX_RETRY", 3),
	}

	// Server
	cfg.Server = ServerConfig{
		HTTPPort: getEnvInt("SERVER_HTTP_PORT", 8080),
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
