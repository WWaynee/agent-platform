package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"agent-platform/api/response"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRecovery(t *testing.T) {
	r := gin.New()
	r.Use(Recovery())

	// 一个故意 panic 的接口
	r.GET("/test/panic", func(c *gin.Context) {
		panic("test panic")
	})

	// 一个正常接口，验证 panic 后服务仍能正常处理其他请求
	r.GET("/test/ok", func(c *gin.Context) {
		response.Success(c, gin.H{"status": "alive"})
	})

	// 1. 调用会 panic 的接口
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test/panic", nil)
	r.ServeHTTP(w, req)

	body := w.Body.String()
	t.Logf("[panic接口] HTTP状态码=%d, 返回体=%s", w.Code, body)

	// 验证返回 HTTP 500
	if w.Code != http.StatusInternalServerError {
		t.Errorf("期望 HTTP 500，实际 %d", w.Code)
	}
	// 验证统一返回结构 code=500
	if !strings.Contains(body, `"code":500`) {
		t.Errorf("返回体缺少 code=500，实际: %s", body)
	}
	// 验证 message
	if !strings.Contains(body, `"message":"服务器内部错误"`) {
		t.Errorf("返回体缺少错误提示，实际: %s", body)
	}

	// 2. 关键验证：panic 被捕获后，服务没有崩溃，还能正常处理下一个请求
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/test/ok", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("panic 后服务应保持可用，但正常接口返回 %d", w2.Code)
	}
	t.Logf("[正常接口] HTTP状态码=%d, 返回体=%s", w2.Code, w2.Body.String())
}
