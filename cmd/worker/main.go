package main

import (
	"encoding/json"
	"log"

	"agent-platform/api/service"
	"agent-platform/config"
	"agent-platform/llmclient"
	"agent-platform/mq"
	"agent-platform/storage"
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
	// 1. 加载配置
	if err := config.Load(); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	cfg := config.GlobalConfig
	log.Println("✅ 配置加载完成")

	// 2. 初始化 MySQL（查任务/文档表）
	if err := storage.InitMySQL(); err != nil {
		log.Fatalf("初始化 MySQL 失败: %v", err)
	}
	log.Println("✅ MySQL 连接成功")

	// 3. 初始化 MinIO（下载待解析文档）
	if err := storage.InitMinIO(); err != nil {
		log.Fatalf("初始化 MinIO 失败: %v", err)
	}
	log.Println("✅ MinIO 连接成功")

	// 4. 初始化 Qdrant（写向量库）
	if err := storage.InitQdrant(storage.DefaultVectorSize); err != nil {
		log.Fatalf("初始化 Qdrant 失败: %v", err)
	}
	log.Println("✅ Qdrant 连接成功")

	// 5. 初始化 LLM 客户端（Embedding 向量化；ProcessDocument 内部以此为基生成向量）
	//    这里先构造一份并校验配置就绪（构造不发起网络调用，APIKey 缺失会在真正 Embedding 时报错）
	_ = llmclient.NewClient(cfg.LLM)
	log.Printf("✅ LLM 客户端就绪 (chat=%s, embed=%s)", cfg.LLM.ChatModel, cfg.LLM.EmbeddingModel)

	// 6. 初始化 RabbitMQ（消费消息源）
	if err := mq.InitRabbitMQ(); err != nil {
		log.Fatalf("初始化 RabbitMQ 失败: %v", err)
	}
	log.Printf("✅ RabbitMQ 连接成功 (vhost=%s, queue=%s)", cfg.RabbitMQ.Vhost, cfg.RabbitMQ.QueueName)

	// 7. 注册消费者：监听 document_parse 队列，阻塞消费其中消息
	//    每收到一条：
	//      - 反序列化消息体（task_id / tenant_id / document_id）
	//      - 调 service.ConsumeDocumentParseTask 执行文档解析 + 任务状态流转
	//      - 返回 nil（成功）→ mq.Consume 会 Ack；返回 error（失败）→ Nack 重新入队
	//      - 重复消费由 ProcessDocument 的幂等点 ID 保证安全（覆盖写，不产生重复向量）
	queue := cfg.RabbitMQ.QueueName
	go func() {
		log.Printf("👷 消费者已启动，监听队列 %q，等待处理任务...", queue)
		if err := mq.Consume(queue, func(body []byte) error {
			var msg mq.DocumentParseMsg
			if err := json.Unmarshal(body, &msg); err != nil {
				log.Printf("⚠️ 解析消息体失败: %v", err)
				return err
			}
			log.Printf("📥 收到任务 task=%d tenant=%d document=%d", msg.TaskID, msg.TenantID, msg.DocumentID)
			return service.ConsumeDocumentParseTask(msg.TaskID, msg.TenantID, msg.DocumentID)
		}); err != nil {
			log.Fatalf("消费者异常退出: %v", err)
		}
	}()

	// 8. 阻塞主协程，等消费者持续工作
	log.Println("🚀 Worker 进入阻塞等待，Ctrl+C 退出")
	select {}
}
