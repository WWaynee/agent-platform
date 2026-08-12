package observability

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"agent-platform/agent/interfaces"
	"agent-platform/config"
)

// ============ 全局结构化日志（zap） ============
//
// 为什么用 zap：
//   - Go 生态最常用、性能好（低分配、零拷贝核心路径）
//   - 支持 JSON 结构化输出，供日志采集系统（ELK / Loki）解析、搜索、过滤
//   - 原生日志分级（Debug / Info / Warn / Error）
//
// 统一字段规范：所有日志固定字段 timestamp/level/msg，以及按场景的
// trace_id/tenant_id/user_id/latency/error（见下方字段常量）。便于采集统一解析。
//
// 设计：
//   - 一个进程一个全局 `Logger`（*zap.Logger）与 `Sugared *zap.SugaredLogger`，
//     启动时经 Init 初始化一次，之后全局调用 Debug/Info/Warn/Error 即可。
//   - 输出格式固定 JSON（生产标准做法，利于结构化采集）。
//   - 日志级别：默认 info（生产），开发环境设 LOG_LEVEL=debug 看最全。
//   - 时间戳用 ISO8601，便于采集系统统一解析。
//   - HTTP/handler 层级建议用 WithContext(ctx) 取 logger，自动带上 trace_id/tenant/user。

// 字段规范常量：方便各层统一引用，避免手写字符串拼错。
const (
	FieldTraceID  = "trace_id"  // 链路 ID（来自 ctx，WithContext 自动注入）
	FieldTenantID = "tenant_id" // 租户 ID（来自 ctx，有则注入）
	FieldUserID   = "user_id"   // 用户 ID（来自 ctx，有则注入）
	FieldLatency  = "latency"   // 请求耗时（毫秒，接口日志用）
	FieldError    = "error"     // 错误信息（error 级别日志用）
)

// 全局 logger（并发安全）。初始化前为空，直接调用会打 nop（不 panic）。
var (
	mu      sync.Mutex
	global  *zap.Logger
	sugared *zap.SugaredLogger
)

// 哨兵：未初始化时的 nop logger，保证任何时刻调用都不 panic、也不崩。
func current() *zap.Logger {
	mu.Lock()
	defer mu.Unlock()
	l := global
	if l == nil {
		// 返回一个丢弃所有日志的 nop logger，兜底
		return zap.NewNop()
	}
	return l
}

// ============ 全局日志方法（无 ctx，字段由调用方补） ============
func Debug(msg string, fields ...zap.Field) { current().Debug(msg, fields...) }
func Info(msg string, fields ...zap.Field)  { current().Info(msg, fields...) }
func Warn(msg string, fields ...zap.Field)  { current().Warn(msg, fields...) }

// Error 打印 error 级别日志，并按规范自动带上 error 字段（err）。
func Error(msg string, err error, fields ...zap.Field) {
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	current().Error(msg, fields...)
}

// ============ Sugared（格式化友好，少量使用） ============
func S() *zap.SugaredLogger {
	mu.Lock()
	defer mu.Unlock()
	if sugared == nil {
		return zap.NewNop().Sugar()
	}
	return sugared
}

// WithContext 从 ctx 提取 trace_id / tenant_id / user_id，自动注入日志字段，
// 返回派生的 *zap.Logger。之后用它打日志即自动带上这些规范字段（有则带、无则省略）。
//
// 用法：
//
//	logger := observability.WithContext(c.Request.Context())
//	logger.Info("业务日志", zap.Int64(observability.FieldLatency, 12))
//	logger.Error("处理失败", zap.String("detail", "..."))
func WithContext(ctx context.Context) *zap.Logger {
	if ctx == nil {
		return current()
	}
	var fields []zap.Field
	if id := interfaces.TraceIDFromCtx(ctx); id != "" {
		fields = append(fields, zap.String(FieldTraceID, id))
	}
	if id := interfaces.TenantIDFromCtx(ctx); id > 0 {
		fields = append(fields, zap.Uint64(FieldTenantID, id))
	}
	if id := interfaces.UserIDFromCtx(ctx); id > 0 {
		fields = append(fields, zap.Uint64(FieldUserID, id))
	}
	return current().With(fields...)
}

// WithTenantUser 直接按租户/用户 ID 构造带日志字段的 logger，
// 供不持有标准 context.Context 的调用方使用（如 Agent 工具执行的 AgentContext）。
// 若两个 ID 都为 0（无身份），等价于返回全局 logger。
func WithTenantUser(tenantID, userID uint64) *zap.Logger {
	var fields []zap.Field
	if tenantID > 0 {
		fields = append(fields, zap.Uint64(FieldTenantID, tenantID))
	}
	if userID > 0 {
		fields = append(fields, zap.Uint64(FieldUserID, userID))
	}
	return current().With(fields...)
}

// WithAgentContext 从 AgentContext 提取租户/用户/trace_id，构造带规范字段的 logger。
//
// 用于 Agent 内部（引擎/工具执行）以 AgentContext 为上下文的场景：比 WithTenantUser
// 多带了 trace_id，使 Agent 链路的 LLM / 工具 / 记忆等日志也能与 HTTP 入口的 trace_id
// 对齐，实现全链路可串联。
//
// 若 trace_id 为空（上层未设置），效果等价于 WithTenantUser。
func WithAgentContext(actx interfaces.AgentContext) *zap.Logger {
	var fields []zap.Field
	if actx.TenantID > 0 {
		fields = append(fields, zap.Uint64(FieldTenantID, actx.TenantID))
	}
	if actx.UserID > 0 {
		fields = append(fields, zap.Uint64(FieldUserID, actx.UserID))
	}
	if tid := actx.TraceID(); tid != "" {
		fields = append(fields, zap.String(FieldTraceID, tid))
	}
	return current().With(fields...)
}

// Init 初始化全局 logger，从 config.GlobalConfig.Log 读取级别。
// 输出格式 JSON。返回一个 sync 函数，退出前调用以刷新缓冲（不调用可能丢最后几条日志）。
func Init() func() {
	return initWith(parseLevel(config.GlobalConfig.Log.Level), os.Stdout, config.GlobalConfig.Log.File)
}

// InitWith 初始化全局 logger 到指定 writer（不落文件）。
// 供测试注入 bytes.Buffer 捕获输出，或自定义输出目标时使用。
// 返回一个 sync 函数，用于刷新缓冲。
func InitWith(out io.Writer, level string) func() {
	return initWith(parseLevel(level), out, "")
}

// initWith 初始化全局 logger。
//   - level：日志级别
//   - out：stdout 输出目标
//   - file：可选日志文件路径（空则不写文件）
//
// 返回一个 sync 函数，用于刷新缓冲。
func initWith(level zapcore.Level, out io.Writer, file string) func() {
	mu.Lock()
	defer mu.Unlock()

	// JSON 编码器：时间戳 ISO8601，key 用字段名，Caller 显示调用点
	encoderCfg := zapcore.EncoderConfig{
		TimeKey:        "timestamp", // 时间字段名（规范化）
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder, // error/info/warn/debug
		EncodeTime:     zapcore.ISO8601TimeEncoder,    // 2026-01-05T10:00:00.000+0800
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	jsonEncoder := zapcore.NewJSONEncoder(encoderCfg)

	core := zapcore.NewCore(jsonEncoder, zapcore.AddSync(out), level)

	// 可选：配了 LOG_FILE 则用 Tee 再加一个文件输出核心（JSON，落盘归档/采集）
	if file != "" {
		if f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fileCore := zapcore.NewCore(zapcore.NewJSONEncoder(encoderCfg), zapcore.AddSync(f), level)
			core = zapcore.NewTee(core, fileCore)
		}
	}

	l := zap.New(core).WithOptions(zap.AddCaller())
	global = l
	sugared = l.Sugar()

	return func() {
		_ = l.Sync() // 刷新缓冲，忽略错误（如 stdout 不可 sync）
	}
}

// parseLevel 把字符串级别解析为 zapcore.Level；非法值回退 info。
func parseLevel(s string) zapcore.Level {
	switch strings.ToLower(s) {
	case "debug":
		return zapcore.DebugLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default: // "" / "info" / 其他
		return zapcore.InfoLevel
	}
}
