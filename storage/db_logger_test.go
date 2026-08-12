package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	gormLogger "gorm.io/gorm/logger"

	"agent-platform/agent/interfaces"
	"agent-platform/observability"
)

// slowBegin 返回一个能触发 dbSlowThreshold 的"开始时刻"（110ms 之前）。
func slowBegin() time.Time { return time.Now().Add(-110 * time.Millisecond) }

// captureDBLog 用 InitWith 把全局 logger 指到 buffer，返回 buffer。
func captureDBLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	_ = observability.InitWith(&buf, "debug")
	return &buf
}

// lastLog 解析出 buffer 里最后一条 JSON 日志 map。
func lastLog(t *testing.T, buf *bytes.Buffer) map[string]interface{} {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("非法 JSON: %q", line)
	}
	return m
}

// TestObsDBLogger_Update logs slow query and error when Trace 返回慢 SQL。
func TestObsDBLogger_Trace_SlowAndError(t *testing.T) {
	log := &obsDBLogger{logLevel: gormLogger.Warn}

	t.Run("慢查询应记录并带 trace_id/tenant_id", func(t *testing.T) {
		buf := captureDBLog(t)
		ctx := interfaces.WithTraceID(context.Background(), "db-trace-1")
		ctx = interfaces.WithTenantUser(ctx, 77, 9)
		log.Trace(ctx, slowBegin(), func() (string, int64) {
			return "SELECT * FROM documents", 3
		}, nil)
		m := lastLog(t, buf)
		if m["msg"] != "DB 慢查询（>=100ms）" {
			t.Errorf("应打慢查询日志，实际 msg=%v", m["msg"])
		}
		if m["trace_id"] != "db-trace-1" {
			t.Errorf("慢查询日志应带 trace_id: %v", m["trace_id"])
		}
		if m["tenant_id"].(float64) != 77 {
			t.Errorf("慢查询日志应带 tenant_id: %v", m["tenant_id"])
		}
	})

	t.Run("错误应记录 error 日志", func(t *testing.T) {
		buf := captureDBLog(t)
		ctx := interfaces.WithTraceID(context.Background(), "db-trace-2")
		log.Trace(ctx, time.Now(), func() (string, int64) {
			return "INSERT INTO documents", 0
		}, errors.New("duplicate key"))
		m := lastLog(t, buf)
		if m["msg"] != "DB 查询失败" {
			t.Errorf("应打错误日志，实际 msg=%v", m["msg"])
		}
		if m["level"] != "error" {
			t.Errorf("级别应为 error: %v", m["level"])
		}
		errStr, _ := m["error"].(string)
		if !strings.Contains(errStr, "duplicate key") {
			t.Errorf("error 字段应含错误信息: %v", m["error"])
		}
	})

	t.Run("ErrRecordNotFound 不应视为错误", func(t *testing.T) {
		buf := captureDBLog(t)
		log.Trace(context.Background(), time.Now(), func() (string, int64) {
			return "SELECT * FROM sessions", 0
		}, gormLogger.ErrRecordNotFound)
		if buf.Len() != 0 {
			t.Errorf("查无记录不应打错误日志，实际有输出:\n%s", buf.String())
		}
	})

	t.Run("ctx 无 tenant 时 slow log 从 SQL 提取 tenant_id", func(t *testing.T) {
		buf := captureDBLog(t)
		// 模拟 GORM 内联参数后的 SQL（含 tenant_id = 数字）
		log.Trace(context.Background(), slowBegin(), func() (string, int64) {
			return "SELECT * FROM documents WHERE tenant_id = 2048 AND is_deleted = 0", 12
		}, nil)
		m := lastLog(t, buf)
		if m["tenant_id"].(float64) != 2048 {
			t.Errorf("SQL 提取 tenant_id 应为 2048，实际 %v", m["tenant_id"])
		}
		if m["msg"] != "DB 慢查询（>=100ms）" {
			t.Errorf("应打慢查询日志，实际 msg=%v", m["msg"])
		}
	})
}

// TestExtractTenantID 验证从内联 SQL 提取 tenant_id 的边界情况。
func TestExtractTenantID(t *testing.T) {
	cases := []struct {
		sql  string
		want uint64
	}{
		{`SELECT * FROM documents WHERE tenant_id = 1001`, 1001},
		{`SELECT * FROM documents WHERE tenant_id='1001'`, 1001}, // 带引号
		{`SELECT * FROM a_tenant_id_score`, 0},                   // 整列名含 tenant_id 前缀：不应误提取
		{`SELECT 1`, 0},                                          // 无租户条件
		{``, 0},
	}
	for _, c := range cases {
		if got := extractTenantID(c.sql); got != c.want {
			t.Errorf("extractTenantID(%q) = %d, want %d", c.sql, got, c.want)
		}
	}
	t.Log("✅ 从 SQL 提取/边界用例通过")
}
