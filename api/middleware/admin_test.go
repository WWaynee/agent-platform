package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 构造一个挂载 AdminAuth 的测试路由，用于单测中间件行为。
// role 参数用于模拟"鉴权中间件从 JWT 解析后注入 context 的 role"。
func newAdminAuthEngine(role string) (*gin.Engine, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/admin/test", func(c *gin.Context) {
		c.Set("role", role) // 模拟 JWTAuth 注入 role → 进 AdminAuth
		c.Next()
	}, AdminAuth(), func(c *gin.Context) {
		c.Status(http.StatusOK) // 走到这里说明中间件已放行
	})
	w := httptest.NewRecorder()
	return r, w
}

// TestAdminAuth_AdminRole 管理员放行
func TestAdminAuth_AdminRole(t *testing.T) {
	r, w := newAdminAuthEngine("admin")
	req := httptest.NewRequest(http.MethodGet, "/api/admin/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("admin 应被放行，期待 200，实际 %d", w.Code)
	}
}

// TestAdminAuth_Member 普通成员 403
func TestAdminAuth_Member(t *testing.T) {
	r, w := newAdminAuthEngine("member")
	req := httptest.NewRequest(http.MethodGet, "/api/admin/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("member 应返回业务 403（HTTP 200 + code 403），实际 HTTP %d", w.Code)
	}
	// 校验业务 code 为 403
	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"message"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Code != 403 {
		t.Errorf("member 期待 code=403，实际 %d", body.Code)
	}
	if body.Msg == "" {
		t.Error("member 被拒时应返回明确提示信息")
	}
}

// TestAdminAuth_NoRole 无 role（中间件顺序异常等）按无权限处理
func TestAdminAuth_NoRole(t *testing.T) {
	r, w := newAdminAuthEngine("")
	req := httptest.NewRequest(http.MethodGet, "/api/admin/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("无 role 应返回业务 403，实际 HTTP %d", w.Code)
	}
	var body struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Code != 403 {
		t.Errorf("无 role 期待 code=403，实际 %d", body.Code)
	}
}
