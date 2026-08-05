package response

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// 测试 Success 返回格式
func TestSuccess(t *testing.T) {
	r := gin.New()
	r.GET("/test/success", func(c *gin.Context) {
		Success(c, gin.H{"name": "agent", "count": 3})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test/success", nil)
	r.ServeHTTP(w, req)

	body := w.Body.String()
	for _, exp := range []string{`"code":0`, `"message":"ok"`, `"data":{"count":3,"name":"agent"}`} {
		if !strings.Contains(body, exp) {
			t.Errorf("Success 返回体缺少 %s，实际: %s", exp, body)
		}
	}
	t.Logf("Success 输出: %s", body)
}

// 测试 Fail 返回格式
func TestFail(t *testing.T) {
	r := gin.New()
	r.GET("/test/fail", func(c *gin.Context) {
		Fail(c, CodeUnauthorized, "未登录")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test/fail", nil)
	r.ServeHTTP(w, req)

	body := w.Body.String()
	for _, exp := range []string{`"code":401`, `"message":"未登录"`, `"data":null`} {
		if !strings.Contains(body, exp) {
			t.Errorf("Fail 返回体缺少 %s，实际: %s", exp, body)
		}
	}
	t.Logf("Fail 输出: %s", body)
}
