package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
	"agent-platform/config"
)

// 默认限流配置（与 config.Load 默认值一致，测试里显式设置以免依赖环境变量）。
func setTestRateLimitConfig() {
	config.GlobalConfig.RateLimit = config.RateLimitConfig{
		TenantPerMin: 300,
		UserPerMin:   60,
		ChatPerMin:   20,
		WindowSec:    60,
		KeyTTL:       120,
	}
}

// TestRateLimiter_WithoutRedis 无 Redis（RDB=nil）时，限流中间件应放行，不崩溃。
// storage.RDB 未初始化或 Redis 挂掉时，AllowRequest 返回 false 即意味着放行，兜底不掉服务。
func TestRateLimiter_WithoutRedis(t *testing.T) {
	setTestRateLimitConfig()

	// 构建带鉴权凭据的 router（模拟已登录，tenant/user 已注入 context）
	r := buildAuthedRouter(1, 1)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ping", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("无 Redis 时不应限流，status=%d, body=%s", w.Code, w.Body.String())
	}
}

// TestRateLimiter_NoTenantID 未取到 tenant_id（=0）时放行，不 abort。
func TestRateLimiter_NoTenantID(t *testing.T) {
	setTestRateLimitConfig()

	r := gin.New()
	r.Use(RateLimiter())
	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})
	// 不注入 tenant_id，让中间件拿到 0

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ping", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("tenant_id=0 时应放行，status=%d", w.Code)
	}
}

// TestAbortTooMany 验证 429 响应体结构。
func TestAbortTooMany(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	abortTooMany(c)
	if c.IsAborted() != true {
		t.Error("abortTooMany 应设置 abort")
	}
	var body response.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Code != 429 {
		t.Errorf("Code = %d, want 429", body.Code)
	}
}

// buildAuthedRouter 构造一个先注入鉴权凭据再执行限流中间件的 router（模拟 JWT 中间件已运行）。
func buildAuthedRouter(tenantID, userID uint64) *gin.Engine {
	withAuth := gin.New()
	withAuth.Use(func(c *gin.Context) {
		c.Set("tenant_id", tenantID)
		c.Set("user_id", userID)
		c.Set("role", "member")
		c.Next()
	})
	withAuth.Use(RateLimiter())
	withAuth.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})
	return withAuth
}
