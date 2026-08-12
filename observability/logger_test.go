package observability

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

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

	// 打几条不同级别 + 结构化字段的日志
	Debug("debug 消息")
	Info("info 消息", zap.String("app", "agent"))
	Warn("warn 消息")
	Error("error 消息")
	S().Infof("sugared 格式化 %s", "ok")

	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 4 {
		t.Fatalf("应有至少 4 条日志，实际 %d 条:\n%s", len(lines), out)
	}

	// 每条都应是合法 JSON，且包含 ts/level/msg/caller 结构字段
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("日志不是合法 JSON: %q", line)
		}
		for _, k := range []string{"ts", "level", "msg", "caller"} {
			if _, ok := m[k]; !ok {
				t.Errorf("JSON 日志缺少字段 %q: %s", k, line)
			}
		}
	}

	t.Logf("✅ 所有日志均为合法 JSON 结构，示例:\n%s", lines[0])
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
