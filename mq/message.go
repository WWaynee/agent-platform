package mq

import (
	"encoding/json"

	"agent-platform/config"
)

// ============ 异步任务消息体定义 ============

// DocumentParseMsg 文档解析任务消息体（投递到 RabbitMQ document_parse 队列）。
//
// 为什么只放「任务 ID / 租户 ID / 文档 ID」而不放文件内容：
//   - 消息体小，传输快、不占队列内存；
//   - 消费者根据这些 ID 去查任务表/文档表/MinIO 拿文件与元数据；
//   - 消息即便延迟被消费，也能根据 ID 读到最新的数据（不会用上"过期"的文件内容）。
type DocumentParseMsg struct {
	TaskID     uint64 `json:"task_id"`     // 任务 ID（agent_tasks.id）
	TenantID   uint64 `json:"tenant_id"`   // 租户 ID
	DocumentID uint64 `json:"document_id"` // 文档 ID（documents.id / biz_id）
}

// PublishDocumentParseTask 把一条文档解析任务投递到配置的文档解析队列。
// 内部自动 JSON 序列化 + 持久化发布到 config 里配的队列名。
// 返回 nil 表示投递成功。
func PublishDocumentParseTask(taskID, tenantID, documentID uint64) error {
	msg := DocumentParseMsg{
		TaskID:     taskID,
		TenantID:   tenantID,
		DocumentID: documentID,
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	queue := config.GlobalConfig.RabbitMQ.QueueName
	return Publish(queue, body)
}
