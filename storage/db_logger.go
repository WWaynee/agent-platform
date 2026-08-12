package storage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"go.uber.org/zap"
	gormLogger "gorm.io/gorm/logger"

	"agent-platform/agent/interfaces"
	"agent-platform/observability"
)

// ============ 数据库（MySQL）慢查询 / 错误日志 ============
//
// 实现 gorm.io/gorm/logger.Interface，把 GORM 的 SQL 执行事件接到 observability：
//   - 出错 → error 日志（error 字段）；
//   - 耗时 ≥ dbSlowThreshold（100ms）→ warn 慢查询日志（latency 字段）；
//   - 其余情况静默（不逐条打 SQL，避免刷屏）。
//
// 关于 trace_id / tenant_id：
//   - ctx 携带标准 context（通过 DB.WithContext(ctx) 传入）时，经 WithContext 自动带 trace_id/tenant_id。
//     但本项目 DAO 多用全局 DB 直接查询（不带 ctx），故 ctx 通常拿不到 tenant_id。
//   - GORM 的 Trace 回调里 fc() 返回的 sql 已由 Dialector.Explain 内联了绑定参数值，
//     本项目所有按租户过滤的 SQL 都带 `tenant_id = <值>` 条件，
//     因此可从内联后的 SQL 中提取 tenant_id 作为日志字段（见 extractTenantID），
//     让慢查询/错误日志在未透传 ctx 时也能定位到具体租户。
//   - 若 future SQL 不再用 `tenant_id = ?` 写法，此提取会缺省，但不影响其他字段。
//
// 为什么在 InitMySQL 注入：
//   - 让"数据库慢查询"也进入统一 JSON 日志，与请求链路日志可关联（同一 trace_id/租户归因）。
//   - GORM 默认 logger 打的是文本格式且写各自 stdout，无法结构化采集。
//
// ⚠️ sql 字段可能包含带查询参数的语句（含 where 条件里的值）。GORM 默认也会打印 SQL，
//
//	这里为便于排查保留；若含敏感查询参数，可自行评估调整。
const dbSlowThreshold = 100 * time.Millisecond

// tenantIDRe 匹配 SQL 中 `tenant_id = <数字>` 的内联参数（GORM 数字参数不带引号，兼容带引号）。
// 用 \b 词边界避免误匹配 `xxx_tenant_id_score` 之类含 tenant_id 前缀的整列名。
var tenantIDRe = regexp.MustCompile(`\btenant_id\s*=\s*'?(\d+)'?`)

// extractTenantID 从一条已内联参数值的 SQL 中提取 tenant_id；提取不到返回 0。
func extractTenantID(sql string) uint64 {
	m := tenantIDRe.FindStringSubmatch(sql)
	if len(m) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// obsDBLogger 是 gorm.Logger.Interface 的 observability 实现。
type obsDBLogger struct {
	logLevel gormLogger.LogLevel
}

// LogMode 设置日志级别（GORM 配置时调用），返回同一实例。
func (l *obsDBLogger) LogMode(level gormLogger.LogLevel) gormLogger.Interface {
	l.logLevel = level
	return l
}

// Info 普通信息日志。
func (l *obsDBLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= gormLogger.Info {
		observability.WithContext(ctx).Info("DB:"+msg, fieldsFromData(data)...)
	}
}

// Warn 警告日志（如 SQL 编译告警）。
func (l *obsDBLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= gormLogger.Warn {
		observability.WithContext(ctx).Warn("DB:"+msg, fieldsFromData(data)...)
	}
}

// Error 数据库错误日志（自动带 error 字段 + trace_id/tenant_id）。
func (l *obsDBLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= gormLogger.Error {
		logger := observability.WithContext(ctx)
		logger.Error("DB 操作错误", append(fieldsFromData(data), zap.Error(joinErr(data)))...)
	}
}

// Trace 每次 SQL 执行后调用：记录慢查询（≥100ms）与错误。
// fc 返回该条 SQL 与影响行数。
func (l *obsDBLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()
	logger := observability.WithContext(ctx)

	base := []zap.Field{
		zap.String("sql", sql),
		zap.Int64("rows", rows),
		zap.Int64(observability.FieldLatency, elapsed.Milliseconds()),
	}
	// 若 ctx 没带 tenant_id（未用 DB.WithContext），则从已内联参数值的 SQL 里提取 tenant_id，
	// 保证慢查询/错误日志始终能定位到具体租户（本项目按租户查询都带 tenant_id = ? 条件）。
	if interfaces.TenantIDFromCtx(ctx) == 0 {
		if tid := extractTenantID(sql); tid > 0 {
			base = append(base, zap.Uint64(observability.FieldTenantID, tid))
		}
	}

	if err != nil && !errors.Is(err, gormLogger.ErrRecordNotFound) {
		// 记录库错误（ErrRecordNotFound 属正常"查无记录"，不视为错误）
		logger.Error("DB 查询失败", append(base, zap.Error(err))...)
		return
	}
	if elapsed >= dbSlowThreshold {
		// ⚠️ 慢查询：达到或超过 100ms
		logger.Warn("DB 慢查询（>=100ms）", base...)
	}
	// 未超过阈值且无错误：不逐条打日志（避免高频 SQL 刷日志）
}

// fieldsFromData 把 GORM 传入的 data 转成扁平 zap 字段（仅简单打印，便于定位）。
func fieldsFromData(data []interface{}) []zap.Field {
	if len(data) == 0 {
		return nil
	}
	out := make([]zap.Field, 0, len(data))
	for i, v := range data {
		out = append(out, zap.Any(fmt.Sprintf("arg%d", i), v))
	}
	return out
}

// joinErr 把 GORM Error 方法的 err(data...最后一个如果是 error 取出) 归一为一个 error。
func joinErr(data []interface{}) error {
	for i := len(data) - 1; i >= 0; i-- {
		if e, ok := data[i].(error); ok {
			return e
		}
	}
	return fmt.Errorf("%v", data)
}
