package mq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"agent-platform/agent/interfaces"
	"agent-platform/config"
	"agent-platform/observability"
)

// ============ 异步任务消息体定义 ============

// DocumentParseMsg 文档解析任务消息体（投递到 RabbitMQ document_parse 队列）。
//
// 为什么只放「任务 ID / 租户 ID / 文档 ID」而不放文件内容：
//   - 消息体小，传输快、不占队列内存；
//   - 消费者根据这些 ID 去查任务表/文档表/MinIO 拿文件与元数据；
//   - 消息即便延迟被消费，也能根据 ID 读到最新的数据（不会用上"过期"的文件内容）。
//
// ▲ trace_id / msg_id 为什么要放进消息体：
//   - msg_id：消息唯一 ID（生产时生成），生产/消费两边日志都用它指代同一条消息，
//     便于按消息粒度追踪（如某条消息消费失败要排查时，直接该 msg_id 全域搜索）。
//   - trace_id：生产方来自 HTTP 请求的链路 ID。把它写进消息体、消费方取出并放回 ctx，
//     就能让"生产者投递日志"与"消费者处理日志"用**同一个 trace_id**串起来，
//     一次上传从 API → MQ → Worker → 落库的完整链路一目了然。
type DocumentParseMsg struct {
	MsgID      string `json:"msg_id"`      // 消息唯一 ID（生产时生成）
	TraceID    string `json:"trace_id"`    // 生产者侧的链路 ID（消费者取出放回 ctx）
	TaskID     uint64 `json:"task_id"`     // 任务 ID（agent_tasks.id）
	TenantID   uint64 `json:"tenant_id"`   // 租户 ID
	DocumentID uint64 `json:"document_id"` // 文档 ID（documents.id / biz_id）
}

// genMsgID 生成一条消息的唯一 ID（32 位十六进制随机串）。
func genMsgID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "msg-fallback-" + time.Now().Format("150405.000000")
	}
	return hex.EncodeToString(b)
}

// PublishDocumentParseTask 把一条文档解析任务投递到配置的文档解析队列。
//
// ctx 需携带当前请求的链路上下文（经 interfaces.WithTraceID 种入的 trace_id）——
// 把它写进消息体，让消费者能把生产与消费日志用同一 trace_id 串起来。
//
// 日志原则（生产方）：
//   - 正常发送 → Info；发送失败 → Error；
//   - 只打关键信息（queue/msg_id/biz_id/trace_id/tenant_id/发送耗时），不打完整消息体；
//   - 消息 ID = msg_id，业务标识 = document_id（biz_id）。
func PublishDocumentParseTask(ctx context.Context, taskID, tenantID, documentID uint64) error {
	msg := DocumentParseMsg{
		MsgID:      genMsgID(),
		TraceID:    interfaces.TraceIDFromCtx(ctx), // 生产侧当前请求的链路 ID
		TaskID:     taskID,
		TenantID:   tenantID,
		DocumentID: documentID,
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	queue := config.GlobalConfig.RabbitMQ.QueueName

	// 生产日志统一基于 ctx（带 trace_id/tenant_id），字段规范、不含消息体。
	logger := observability.WithContext(ctx)
	// 若 ctx 未带租户（上游未注入），用消息里的租户兜底，保证按租户可定位。
	if interfaces.TenantIDFromCtx(ctx) == 0 {
		logger = observability.WithTenantUser(tenantID, 0)
	}

	// 发送耗时
	start := time.Now()
	err = Publish(queue, body)
	fields := []zap.Field{
		zap.String("queue", queue),
		zap.String("msg_id", msg.MsgID),
		zap.Uint64("biz_id", documentID),
		zap.Uint64("task_id", taskID),
		zap.Int64(observability.FieldLatency, time.Since(start).Milliseconds()),
	}
	if err != nil {
		logger.Error("MQ 消息投递失败", append(fields, zap.Error(err))...)
		return err
	}
	logger.Info("MQ 消息投递成功", fields...)
	return nil
}
