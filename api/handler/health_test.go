package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"agent-platform/api/service"
	"agent-platform/mq"
	"agent-platform/storage"
)

// TestHealthHandler_DownReturns503 验证：依赖不可用（全局对象未初始化）时，
// /health 返回 HTTP 503，响应体为健康报告结构（overall=down、含各 down 组件与错误信息）。
func TestHealthHandler_DownReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 保存并清空全局依赖，模拟"服务依赖全部不可用（故意停掉）"
	origDB, origRDB := storage.DB, storage.RDB
	origMinio, origQdrant := storage.OSSClient, storage.QdrantClient
	origConn, origCh := mq.MQConn, mq.MQCh
	storage.DB, storage.RDB, storage.OSSClient, storage.QdrantClient = nil, nil, nil, nil
	mq.MQConn, mq.MQCh = nil, nil
	defer func() {
		storage.DB, storage.RDB, storage.OSSClient, storage.QdrantClient = origDB, origRDB, origMinio, origQdrant
		mq.MQConn, mq.MQCh = origConn, origCh
	}()

	router := gin.New()
	router.GET("/health", Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("依赖 down 时 /health 应返回 503，实际 %d", w.Code)
	}

	var report service.HealthReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}

	// 顶层 status 应为 unhealthy
	if report.Status != "unhealthy" {
		t.Errorf("status 应为 unhealthy，实际 %q", report.Status)
	}
	// components 应为字符串值 map，包含 5 个组件且每个都 "down"；
	// errors map 应包含每个 down 组件的错误信息。
	if len(report.Components) != 5 {
		t.Errorf("应返回 5 个组件，实际 %d", len(report.Components))
	}
	for _, name := range []string{"mysql", "redis", "oss", "qdrant", "rabbitmq"} {
		status, ok := report.Components[name]
		if !ok {
			t.Errorf("缺少组件 %s", name)
			continue
		}
		if status != "down" {
			t.Errorf("依赖不可用时组件 %s 应为 down，实际 %q", name, status)
		}
		if report.Errors[name] == "" {
			t.Errorf("组件 %s down 应带错误信息 (errors[%s])", name, name)
		}
	}

	t.Log("✅ 依赖不可用时 /health 返回 503，status=unhealthy，components 各组件 down、errors 带错误信息")
}
