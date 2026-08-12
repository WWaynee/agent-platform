package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"agent-platform/agent/interfaces"
	"agent-platform/config"

	"go.uber.org/zap"
)

// initForTest 用给定的 buffer 初始化 logger（避免污染测试 stdout），返回 buffer。
func initForTest(buf *bytes.Buffer, level string) {
	config.GlobalConfig.Log = config.LogConfig{Level: level}
	_ = initWith(parseLevel(level), buf, "")
}

// TestLogger_InitAndJSON 验证：初始化成功，且输出的是合法 JSON 行。
func TestLogger_InitAndJSON(t *testing.T) {
	var buf bytes.Buffer
	initForTest(&buf, "debug")
	if global == nil || sugared == nil {
		t.Fatal("日志初始化后 global/sugared 不应为 nil")
	}

	// 打几条不同级别 + 结构化字段的日志（验证统一字段规范）
	Debug("debug 消息")
	Info("info 消息", zap.String("app", "agent"))
	Warn("warn 消息")
	Error("error 消息", errors.New("boom"))
	S().Infof("sugared 格式化 %s", "ok")

	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 {
		t.Fatalf("应有至少 4 条日志，实际 %d 条:\n%s", len(lines), out)
	}

	// 每条都应是合法 JSON，且包含规范字段 timestamp/level/msg
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("日志不是合法 JSON: %q", line)
		}
		for _, k := range []string{"timestamp", "level", "msg"} {
			if _, ok := m[k]; !ok {
				t.Errorf("JSON 日志缺少规范字段 %q: %s", k, line)
			}
		}
	}

	t.Logf("✅ 所有日志均为合法 JSON 且含统一字段 timestamp/level/msg，示例:\n%s", lines[0])
}

// TestLogger_Levels 验证：不同级别日志按级别正确输出、debug 级别生效。
func TestLogger_Levels(t *testing.T) {
	var buf bytes.Buffer
	initForTest(&buf, "debug")
	Debug("debug-出现")
	Info("info-出现")
	Warn("warn-出现")

	out := buf.String()
	if !strings.Contains(out, "debug-出现") {
		t.Error("debug 级别下应输出 Debug 日志")
	}
	if !strings.Contains(out, "info-出现") {
		t.Error("应输出 Info 日志")
	}
	if !strings.Contains(out, "warn-出现") {
		t.Error("应输出 Warn 日志")
	}
	t.Log("✅ debug 级别下 Debug/Info/Warn 均正常输出")
}

// TestLogger_LevelGate 验证：默认 info 级别下，debug 日志被过滤（级别门控生效）。
func TestLogger_LevelGate(t *testing.T) {
	var buf bytes.Buffer
	initForTest(&buf, "info") // info 级别
	Debug("不该出现的debug")
	Info("该出现的info")

	out := buf.String()
	if strings.Contains(out, "不该出现的debug") {
		t.Error("info 级别下不应输出 debug 日志")
	}
	if !strings.Contains(out, "该出现的info") {
		t.Error("info 级别下应输出 info 日志")
	}
	t.Log("✅ 级别门控生效：info 级别下 debug 被过滤、info 保留")
}

// TestLogger_StructuredFields 验证：结构化字段 key/value 都被打进 JSON。
func TestLogger_StructuredFields(t *testing.T) {
	var buf bytes.Buffer
	initForTest(&buf, "debug")
	Info("业务日志",
		zap.Uint64("tenant_id", 42),
		zap.Int64("latency_ms", 123),
	)

	var m map[string]interface{}
	line := strings.TrimSpace(buf.String())
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("非法 JSON: %q", line)
	}
	if v := m["tenant_id"]; v == nil || v.(float64) != 42 {
		t.Errorf("tenant_id 字段缺失或不对: %v", m["tenant_id"])
	}
	if v := m["latency_ms"]; v == nil || v.(float64) != 123 {
		t.Errorf("latency_ms 字段缺失或不对: %v", m["latency_ms"])
	}
	t.Log("✅ 结构化字段正确打进 JSON")
}

// TestLogger_Concurrent 验证：并发调用全局日志不 panic、不失效。
func TestLogger_Concurrent(t *testing.T) {
	var buf bytes.Buffer
	initForTest(&buf, "debug")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			Debug("并发", zap.Int("i", i))
		}(i)
	}
	wg.Wait()
	if !strings.Contains(buf.String(), "并发") {
		t.Error("并发日志应有输出")
	}
	t.Log("✅ 并发调用正常")
}

// TestLogger_WithContext 验证：WithContext 从 ctx 自动提取并注入 trace_id/tenant_id/user_id。
func TestLogger_WithContext(t *testing.T) {
	var buf bytes.Buffer
	initForTest(&buf, "debug")

	// 构造携带规范字段的 ctx
	ctx := context.Background()
	ctx = interfaces.WithTenantUser(ctx, 1001, 202)
	ctx = interfaces.WithTraceID(ctx, "trace-abc-123")

	logger := WithContext(ctx)
	logger.Info("带上下文的业务日志", zap.Int64(FieldLatency, 88))

	var m map[string]interface{}
	line := strings.TrimSpace(buf.String())
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("非法 JSON: %q", line)
	}
	if m["trace_id"] != "trace-abc-123" {
		t.Errorf("trace_id 应自动注入 = %v", m["trace_id"])
	}
	if m["tenant_id"].(float64) != 1001 {
		t.Errorf("tenant_id 应自动注入 = %v", m["tenant_id"])
	}
	if m["user_id"].(float64) != 202 {
		t.Errorf("user_id 应自动注入 = %v", m["user_id"])
	}
	if m["latency"].(float64) != 88 {
		t.Errorf("latency 应保留 = %v", m["latency"])
	}
	t.Log("✅ WithContext 自动带上了 trace_id/tenant_id/user_id 且保留调用方字段")

	// 空 ctx 不应 panic，且不注入任何规范字段
	var buf2 bytes.Buffer
	initForTest(&buf2, "debug")
	WithContext(nil).Info("无 ctx 日志")
	if !strings.Contains(buf2.String(), "无 ctx 日志") {
		t.Error("nil ctx 应能正常打日志")
	}
}

// TestLogger_ErrorField 验证：Error(msg, err, ...) 自动带 error 字段且级别为 error。
func TestLogger_ErrorField(t *testing.T) {
	var buf bytes.Buffer
	initForTest(&buf, "debug")
	Error("操作失败", errors.New("磁盘写入错误"), zap.String("stage", "save"))

	var m map[string]interface{}
	line := strings.TrimSpace(buf.String())
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("非法 JSON: %q", line)
	}
	if m["level"] != "error" {
		t.Errorf("级别应为 error，实际 %v", m["level"])
	}
	if m["stage"] != "save" {
		t.Errorf("其余字段应保留 = %v", m["stage"])
	}
	errStr, ok := m["error"].(string)
	if !ok || !strings.Contains(errStr, "磁盘写入错误") {
		t.Errorf("error 字段应包含错误信息 = %v", m["error"])
	}
	t.Log("✅ Error 自动带 error 字段、级别 error")
}

// TestLogger_ErrorNil 验证：Error(msg, nil) 不产生 error 字段也不 panic。
func TestLogger_ErrorNil(t *testing.T) {
	var buf bytes.Buffer
	initForTest(&buf, "debug")
	Error("无错误的 error 日志", nil)

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &m); err != nil {
		t.Fatalf("非法 JSON: %q", buf.String())
	}
	if _, hasErr := m["error"]; hasErr {
		t.Errorf("err 为 nil 时不应有 error 字段")
	}
	if m["level"] != "error" {
		t.Errorf("级别应为 error")
	}
	t.Log("✅ Error(..., nil) 正常且无 error 字段")
}
