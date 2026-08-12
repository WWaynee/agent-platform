package mq

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agent-platform/agent/interfaces"
	"agent-platform/config"
	"agent-platform/observability"
)

// setupQueueName 临时设置 RabbitMQ 队列名配置（PublishDocumentParseTask 取它做目标队列）。
func setupQueueName(t *testing.T, queue string) {
	t.Helper()
	cfg := config.GlobalConfig
	cfg.RabbitMQ.QueueName = queue
	config.GlobalConfig = cfg
}

// setupObs 把全局 logger 指到 buffer，便于断言日志。
func setupObs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	_ = observability.InitWith(&buf, "debug")
	return &buf
}

// TestGenMsgID_Unique 验证每次生成的消息 ID 不重复。
func TestGenMsgID_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := genMsgID()
		if id == "" {
			t.Fatal("msg_id 不应为空")
		}
		if seen[id] {
			t.Fatalf("msg_id 重复: %s", id)
		}
		seen[id] = true
	}
	t.Log("✅ 100 次生成的 msg_id 均唯一")
}

// TestDocumentParseMsg_ContainsTraceID 验证消息体序列化后保留了 msg_id / trace_id /
// task_id / tenant_id / document_id 关键标识，且不含文件内容等业务数据。
func TestDocumentParseMsg_ContainsTraceID(t *testing.T) {
	msg := DocumentParseMsg{
		MsgID:      "m-1",
		TraceID:    "trace-prod-xyz",
		TaskID:     42,
		TenantID:   88,
		DocumentID: 7,
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	for _, k := range []string{"msg_id", "trace_id", "task_id", "tenant_id", "document_id"} {
		if _, ok := got[k]; !ok {
			t.Errorf("消息体缺少字段 %q", k)
		}
	}
	if got["trace_id"] != "trace-prod-xyz" {
		t.Errorf("trace_id 应被埋入消息体: %v", got["trace_id"])
	}
	if got["msg_id"] != "m-1" {
		t.Errorf("msg_id 应被埋入消息体: %v", got["msg_id"])
	}
	t.Log(fmtJSON(t, body))
}

// TestPublishDocumentParseTask_LogsWithTrace 验证：即便投递失败，
// 生产日志也会（①）带当前请求的 trace_id（① 来自 ctx）、② 级别为 error、③ 报错字段带 error。
func TestPublishDocumentParseTask_LogsWithTrace(t *testing.T) {
	buf := setupObs(t)
	setupQueueName(t, "test_document_parse")

	// 让 Publish 走失败路径：MQCh 未初始化（nil）→ Publish 返回错误 → 打 Error 日志
	oldCh := MQCh
	MQCh = nil

	// 构造带 trace_id 的 ctx（模拟 HTTP 请求链路上游已种入）
	ctx := interfaces.WithTraceID(context.Background(), "trace-api-123")
	ctx = interfaces.WithTenantUser(ctx, 1001, 0)

	err := PublishDocumentParseTask(ctx, 42, 1001, 7)
	if err == nil {
		t.Fatal("MQCh 为 nil 时应返回错误")
	}

	// 恢复全局 MQCh
	MQCh = oldCh

	out := buf.String()
	if !strings.Contains(out, `"level":"error"`) {
		t.Errorf("投递失败应打 error 级别日志:\n%s", out)
	}
	if !strings.Contains(out, "trace-api-123") {
		t.Errorf("生产日志应带当前请求 trace_id:\n%s", out)
	}
	if !strings.Contains(out, "1001") {
		t.Errorf("生产日志应带 tenant_id:\n%s", out)
	}
	if !strings.Contains(out, "MQ 消息投递失败") {
		t.Errorf("应打投递失败日志:\n%s", out)
	}
	t.Log("✅ 投递失败日志带 trace_id / tenant_id / error 字段")
}

// fmtJSON 仅用于测试日志展示（把 JSON 行原样输出，不拼完整 body，避免泄露不可控内容）。
func fmtJSON(t *testing.T, body []byte) string {
	return string(body)
}
