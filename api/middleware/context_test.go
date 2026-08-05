package middleware

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestContextGetTenantID(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)

	// 正常情况：能取到
	c.Set("tenant_id", uint64(5))
	if got := GetTenantID(c); got != 5 {
		t.Errorf("GetTenantID = %d, want 5", got)
	}

	// 类型不匹配（存入 string）：不应 panic，返回 0
	c.Set("tenant_id", "not-a-uint64")
	if got := GetTenantID(c); got != 0 {
		t.Errorf("类型不匹配时应返回 0，实际 %d", got)
	}

	// 未设置 key：不应 panic，返回 0
	empty, _ := gin.CreateTestContext(nil)
	if got := GetTenantID(empty); got != 0 {
		t.Errorf("未设置时应返回 0，实际 %d", got)
	}
}

func TestContextGetUserID(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set("user_id", uint64(42))
	if got := GetUserID(c); got != 42 {
		t.Errorf("GetUserID = %d, want 42", got)
	}

	// 类型不匹配
	c.Set("user_id", uint32(9))
	if got := GetUserID(c); got != 0 {
		t.Errorf("类型不匹配时应返回 0，实际 %d", got)
	}
}

func TestContextGetRole(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set("role", "admin")
	if got := GetRole(c); got != "admin" {
		t.Errorf("GetRole = %q, want admin", got)
	}

	// 类型不匹配（存入非字符串）
	c.Set("role", 123)
	if got := GetRole(c); got != "" {
		t.Errorf("类型不匹配时应返回空串，实际 %q", got)
	}
}
