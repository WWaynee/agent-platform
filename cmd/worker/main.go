package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"agent-platform/agent/interfaces"
	"agent-platform/api/service"
	"agent-platform/config"
	"agent-platform/llmclient"
	"agent-platform/mq"
	"agent-platform/observability"
	"agent-platform/storage"

	"go.uber.org/zap"
)

// ============ Worker：独立消费者进程 ============
//
// 作用：从 RabbitMQ 的 document_parse 队列消费"文档解析任务"消息，
//       独立于 API 服务的后台进程。API 负责接收请求投递消息，
//       Worker 负责真正执行文档解析（切片→Embedding→写Qdrant→更新状态）。
//
// 为什么独立进程：
//   - 与 API 服务解耦：API 挂了不影响已在队列里的任务继续消费；
//   - 可单独扩容消费者：起多个 Worker 实例即可水平扩展处理能力；
//   - 职责分离：API 接请求、Worker 干活。
//
// ⚠️ 使用：go run cmd/worker/main.go（在 .env 就绪、中间件(MySQL/MinIO/Qdrant/RabbitMQ)起来后启动）。

func main() {
	// 0. 加载配置（先于日志初始化，因为日志级别来自配置）
	if err := config.Load(); err != nil {
		// 日志尚未初始化，用标准 log 快速暴露启动失败
		fatalLog("加载配置失败: %v", err)
	}
	cfg := config.GlobalConfig

	// 0.5 初始化结构化 JSON 日志（读 LOG_LEVEL / LOG_FILE），退出前刷新缓冲
	obsFlush := observability.Init()
	defer obsFlush()
	observability.Info("配置加载完成")

	// 1. 初始化 MySQL（查任务/文档表）
	if err := storage.InitMySQL(); err != nil {
		fatalLog("初始化 MySQL 失败: %v", err)
	}
	observability.Info("MySQL 连接成功")

	// 2. 初始化 MinIO（下载待解析文档）
	if err := storage.InitMinIO(); err != nil {
		fatalLog("初始化 MinIO 失败: %v", err)
	}
	observability.Info("MinIO 连接成功")

	// 3. 初始化 Qdrant（写向量库）
	if err := storage.InitQdrant(storage.DefaultVectorSize); err != nil {
		fatalLog("初始化 Qdrant 失败: %v", err)
	}
	observability.Info("Qdrant 连接成功")

	// 4. 初始化 LLM 客户端（Embedding 向量化；ProcessDocument 内部以此为基生成向量）
	_ = llmclient.NewClient(cfg.LLM)
	observability.Info("LLM 客户端就绪",
		zap.String("chat_model", cfg.LLM.ChatModel),
		zap.String("embed_model", cfg.LLM.EmbeddingModel))

	// 5. 初始化 RabbitMQ（消费消息源）
	if err := mq.InitRabbitMQ(); err != nil {
		fatalLog("初始化 RabbitMQ 失败: %v", err)
	}
	observability.Info("RabbitMQ 连接成功",
		zap.String("vhost", cfg.RabbitMQ.Vhost),
		zap.String("queue", cfg.RabbitMQ.QueueName))

	// 6. 注册消费者：监听 document_parse 队列，阻塞消费其中消息
	//    每收到一条：
	//      - 反序列化消息体（含 msg_id / trace_id / task_id / tenant_id / document_id）
	//      - 从消息体取出 trace_id 放进消费上下文（WithTraceID），使本次消费全链路日志
	//        与该消息"生产者投递时的 trace_id"一致，生产/消费日志用同一链路 ID 串起来；
	//      - 调 service.ConsumeDocumentParseTask(ctx, ...) 执行文档解析 + 任务状态流转
	//      - 返回 nil（成功）→ mq.Consume 会 Ack；返回 error（失败）→ Nack 重新入队
	//      - 重复消费由 ProcessDocument 的幂等点 ID 保证安全（覆盖写，不产生重复向量）
	//   日志：收到消息/处理完成记录 queue/msg_id/biz_id/trace_id/耗时，不含消息体。
	queue := cfg.RabbitMQ.QueueName
	go func() {
		observability.Info("消费者已启动，监听队列", zap.String("queue", queue))
		if err := mq.Consume(queue, func(body []byte) error {
			// 反序列化失败：此阶段还拿不到 msg_id/trace_id，用队列名兜底记录
			var msg mq.DocumentParseMsg
			if err := json.Unmarshal(body, &msg); err != nil {
				observability.Error("解析消息体失败", err, zap.String("queue", queue))
				return err
			}

			// 从消息体取 trace_id + tenant_id 构造消费上下文：
			// WithContext 会自动带出 trace_id / tenant_id，整条消费日志统一携带。
			consumeCtx := interfaces.WithTraceID(context.Background(), msg.TraceID)
			consumeCtx = interfaces.WithTenantUser(consumeCtx, msg.TenantID, 0)
			logger := observability.WithContext(consumeCtx)

			// msg_id：兼容旧消息（无 msg_id 时用 task_id 兜底，保证可定位）
			msgID := msg.MsgID
			if msgID == "" {
				msgID = fmt.Sprintf("task-%d", msg.TaskID)
			}
			logFields := []zap.Field{
				zap.String("queue", queue),
				zap.String("msg_id", msgID),
				zap.Uint64("biz_id", msg.DocumentID),
				zap.Uint64("task_id", msg.TaskID),
			}

			// ① 收到消息日志（消费方）：queue / msg_id / biz_id / task_id
			logger.Info("MQ 消息接收", logFields...)

			// ② 处理，记录处理耗时（与发送耗时区分，方便定位是 MQ 慢还是处理慢）
			start := time.Now()
			perr := service.ConsumeDocumentParseTask(consumeCtx, msg.TaskID, msg.TenantID, msg.DocumentID)
			fields := append(logFields,
				zap.Int64(observability.FieldLatency, time.Since(start).Milliseconds()))
			if perr != nil {
				logger.Error("MQ 消息处理失败", append(fields, zap.Error(perr))...)
			} else {
				logger.Info("MQ 消息处理成功", fields...)
			}
			return perr
		}); err != nil {
			fatalLog("消费者异常退出: %v", err)
		}
	}()

	// 7. 阻塞主协程，等消费者持续工作
	observability.Info("Worker 进入阻塞等待，Ctrl+C 退出")
	select {}
}

// fatalLog 启动期致命错误：先打日志再退出。
// 说明：config.Load 失败时 observability 尚未初始化，此处用标准库 log 兜底；
// 其余场景路径下 observability 已可用，本函数优先走结构化日志。
func fatalLog(format string, args ...interface{}) {
	observability.S().Errorf("启动失败: "+format, args...)
	os.Exit(1)
}
