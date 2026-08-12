package observability

import (
	"io"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"agent-platform/config"
)

// ============ 全局结构化日志（zap） ============
//
// 为什么用 zap：
//   - Go 生态最常用、性能好（低分配、零拷贝核心路径）
//   - 支持 JSON 结构化输出，供日志采集系统（ELK / Loki）解析、搜索、过滤
//   - 原生日志分级（Debug / Info / Warn / Error）
//
// 设计：
//   - 一个进程一个全局 `Logger`（*zap.Logger）与 `Sugared *zap.SugaredLogger`，
//     启动时经 Init 初始化一次，之后全局调用 Debug/Info/Warn/Error 即可。
//   - 输出格式固定 JSON（生产标准做法，利于结构化采集）。
//   - 日志级别：默认 info（生产），开发环境设 LOG_LEVEL=debug 看最全。
//   - 时间戳用 ISO8601，便于采集系统统一解析。
//   - 调用方大多不需要手动 zap，用 SugaredLogger 的格式化接口最顺手
//     （如 observe.Info("处理 %d 条消息", n) / observe.Errorw("失败", "err", err)）。

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

// Info 打印 info 级别日志（可用于非结构化、带格式化参数）。
func Info(msg string, fields ...zap.Field)  { current().Info(msg, fields...) }
func Debug(msg string, fields ...zap.Field) { current().Debug(msg, fields...) }
func Warn(msg string, fields ...zap.Field)  { current().Warn(msg, fields...) }
func Error(msg string, fields ...zap.Field) { current().Error(msg, fields...) }

// ============ Sugared（格式化友好） ============
func S() *zap.SugaredLogger {
	mu.Lock()
	defer mu.Unlock()
	if sugared == nil {
		return zap.NewNop().Sugar()
	}
	return sugared
}

// Init 初始化全局 logger，从 config.GlobalConfig.Log 读取级别。
// 输出格式 JSON。返回一个 sync 函数，退出前调用以刷新缓冲（不调用可能丢最后几条日志）。
func Init() func() {
	return initWith(parseLevel(config.GlobalConfig.Log.Level), os.Stdout, config.GlobalConfig.Log.File)
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
		TimeKey:        "ts",
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
