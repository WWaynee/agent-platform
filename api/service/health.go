package service

import (
	"agent-platform/config"
	"agent-platform/mq"
	"agent-platform/storage"
	"context"
	"time"
)

// ============ 依赖健康检查（/health 详细版） ============
//
// 为什么做详细健康检查（而不是 /health 简单返回 running）：
//   - 部署时 K8s / Docker 探针据此判断服务是否真的「可用」，而非进程还活着；
//   - 出问题时一眼看出是哪个依赖挂了（MySQL / Redis / MinIO / Qdrant / RabbitMQ），
//     不用逐个排查；
//   - 整体状态：任一依赖 down → 整体不健康（HTTP 503），让负载均衡/编排剔除该实例。
//
// 返回结构（与 /health 接口约定一致）：
//   {
//     "status": "healthy",              // 全部 up = healthy；任一 down = unhealthy
//     "components": {
//       "mysql":    "up",
//       "redis":    "down",
//       "oss":      "up",
//       ...
//     },
//     "errors": {                        // 仅 down 时存在，key=组件名
//       "redis": "dial tcp: connect: connection refused"
//     }
//   }
//
// components 值统一为字符串 "up"/"down"（便于探针/前端简单解析）；
// 具体错误信息单独放在 errors（key=组件名，仅包含 down 的组件）。
// 全局对象未初始化（如某依赖未启动被 Init 跳过）也判定为 down，并写入 errors。

// 各依赖健康检查的超时：避免某个依赖 HANG 导致健康检查自身卡死（探针超时可能误判整机挂掉）。
const healthCheckTimeout = 3 * time.Second

// 组件状态值常量。
const (
	StatusUp   = "up"   // 组件健康
	StatusDown = "down" // 组件故障
)

// checkResult 单个组件检查的内部分层结果（含错误信息，由 CheckAll 拆分成对外两个 map）。
type checkResult struct {
	status string // "up" / "down"
	errMsg string // down 时的错误信息（up 时为空）
}

// isUp 判断该组件是否健康。
func (c checkResult) isUp() bool {
	return c.status == StatusUp
}

// HealthReport 一次健康检查的完整结果（/health 对外返回结构）。
type HealthReport struct {
	// Status 整体状态："healthy"（全部组件正常）/ "unhealthy"（存在任一组件故障）。
	Status string `json:"status"`
	// Components 各组件状态，key 为组件名，value 为 "up" / "down"。
	Components map[string]string `json:"components"`
	// Errors 各 down 组件的错误信息，key 为组件名；无 down 组件时省略。
	Errors map[string]string `json:"errors,omitempty"`
}

// IsHealthy 返回整体是否健康（全部组件 up 才 true）。
func (r HealthReport) IsHealthy() bool {
	return r.Status == "healthy"
}

// withTimeout 返回带健康检查超时的 context。
func withTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), healthCheckTimeout)
}

// CheckMySQL 检查 MySQL：执行 SELECT 1。
func CheckMySQL(ctx context.Context) checkResult {
	if storage.DB == nil {
		return checkResult{status: StatusDown, errMsg: "MySQL 未初始化"}
	}
	var one int
	// 用原生 SQL 执行 SELECT 1，验证数据库真的能响应（而非只验证连接池内存态）。
	if err := storage.DB.WithContext(ctx).Raw("SELECT 1").Scan(&one).Error; err != nil {
		return checkResult{status: StatusDown, errMsg: err.Error()}
	}
	if one != 1 {
		return checkResult{status: StatusDown, errMsg: "SELECT 1 结果异常"}
	}
	return checkResult{status: StatusUp}
}

// CheckRedis 检查 Redis：执行 PING。
func CheckRedis(ctx context.Context) checkResult {
	if storage.RDB == nil {
		return checkResult{status: StatusDown, errMsg: "Redis 未初始化"}
	}
	if err := storage.RDB.Ping(ctx).Err(); err != nil {
		return checkResult{status: StatusDown, errMsg: err.Error()}
	}
	return checkResult{status: StatusUp}
}

// CheckMinIO 检查对象存储（OSS，需求单 0010 后对接阿里云 OSS）：确认默认 bucket 存在。
// 函数名沿用历史 CheckMinIO；组件展示名统一为 "oss"。
func CheckMinIO(ctx context.Context) checkResult {
	if storage.OSSClient == nil {
		return checkResult{status: StatusDown, errMsg: "OSS 未初始化"}
	}
	bucket := config.GlobalConfig.OSS.Bucket
	exists, err := storage.OSSClient.IsBucketExist(ctx, bucket)
	if err != nil {
		return checkResult{status: StatusDown, errMsg: err.Error()}
	}
	if !exists {
		return checkResult{status: StatusDown, errMsg: "bucket 不存在: " + bucket}
	}
	return checkResult{status: StatusUp}
}

// CheckQdrant 检查 Qdrant：查询目标集合是否存在（能响应即视为服务可用）。
func CheckQdrant(ctx context.Context) checkResult {
	if storage.QdrantClient == nil {
		return checkResult{status: StatusDown, errMsg: "Qdrant 未初始化"}
	}
	collection := config.GlobalConfig.Qdrant.CollectionName
	if _, err := storage.QdrantClient.CollectionExists(ctx, collection); err != nil {
		return checkResult{status: StatusDown, errMsg: err.Error()}
	}
	return checkResult{status: StatusUp}
}

// CheckRabbitMQ 检查 RabbitMQ：连接是否处于打开状态，并能创建轻量 Channel。
func CheckRabbitMQ(ctx context.Context) checkResult {
	if mq.MQConn == nil {
		return checkResult{status: StatusDown, errMsg: "RabbitMQ 未初始化"}
	}
	if mq.MQConn.IsClosed() {
		return checkResult{status: StatusDown, errMsg: "RabbitMQ 连接已关闭"}
	}
	// 尝试打开一个临时 Channel，确认连接可真正用于消息收发（更严格）。
	ch, err := mq.MQConn.Channel()
	if err != nil {
		return checkResult{status: StatusDown, errMsg: "创建 Channel 失败: " + err.Error()}
	}
	_ = ch.Close()
	return checkResult{status: StatusUp}
}

// CheckAll 检查全部依赖，返回完整健康报告。
// 整体状态：只要有一个 down，即为整体 unhealthy。
func CheckAll() HealthReport {
	ctx, cancel := withTimeout()
	defer cancel()

	results := map[string]checkResult{
		"mysql":    CheckMySQL(ctx),
		"redis":    CheckRedis(ctx),
		"oss":      CheckMinIO(ctx),
		"qdrant":   CheckQdrant(ctx),
		"rabbitmq": CheckRabbitMQ(ctx),
	}

	report := HealthReport{
		Components: make(map[string]string, len(results)),
		Errors:     make(map[string]string),
	}

	// 拆成对外结构：components = 组件名 → "up"/"down"；errors = down 组件 → 错误信息。
	for name, r := range results {
		report.Components[name] = r.status
		if !r.isUp() && r.errMsg != "" {
			report.Errors[name] = r.errMsg
		}
	}
	if len(report.Errors) == 0 {
		report.Errors = nil // 无 down：省略 errors 字段
	}

	if overallUp(results) {
		report.Status = "healthy"
	} else {
		report.Status = "unhealthy"
	}
	return report
}

// overallUp 根据各组件状态判断整体是否健康：任一 down → false；全部 up → true。
// 抽成独立纯函数便于单测（不依赖真实依赖）。
func overallUp(results map[string]checkResult) bool {
	for _, r := range results {
		if !r.isUp() {
			return false
		}
	}
	return true
}
