package util

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"agent-platform/config"
)

func TestMain(m *testing.M) {
	// 加载配置（填充 config.GlobalConfig.JWT.Secret / ExpireSeconds）
	_ = config.Load()
	m.Run()
}

// 测试1：生成的 token 是一串长长的字符串
func TestGenerateTokenIsLongString(t *testing.T) {
	token, err := GenerateToken(1, 2, "admin")
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}

	if len(token) < 20 {
		t.Errorf("token 长度异常，应该是较长的字符串，实际长度为 %d", len(token))
	}
	t.Logf("token: %s...", token[:30])
}

// 测试2：解析能拿到正确的 user_id、tenant_id、role
func TestParseTokenGetsCorrectClaims(t *testing.T) {
	var (
		wantUserID   uint64 = 100
		wantTenantID uint64 = 200
		wantRole            = "member"
	)

	token, err := GenerateToken(wantUserID, wantTenantID, wantRole)
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}

	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("解析 token 失败: %v", err)
	}

	if claims.UserID != wantUserID {
		t.Errorf("UserID 不匹配: got %d, want %d", claims.UserID, wantUserID)
	}
	if claims.TenantID != wantTenantID {
		t.Errorf("TenantID 不匹配: got %d, want %d", claims.TenantID, wantTenantID)
	}
	if claims.Role != wantRole {
		t.Errorf("Role 不匹配: got %s, want %s", claims.Role, wantRole)
	}
}

// 测试3：过期 token 解析失败
func TestParseTokenExpired(t *testing.T) {
	// 手动构造一个已过期的 token（ExpiresAt 在过去）
	claims := &Claims{
		UserID:   100,
		TenantID: 200,
		Role:     "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)), // 1 小时前已过期
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(config.GlobalConfig.JWT.Secret))
	if err != nil {
		t.Fatalf("构造过期 token 失败: %v", err)
	}

	// 解析应失败（过期）
	_, err = ParseToken(tokenStr)
	if err == nil {
		t.Errorf("过期 token 应解析失败，但没有返回错误")
	}
	t.Logf("过期 token 解析失败(符合预期): %v", err)
}

// 测试4：篡改后的 token（签名不匹配）解析失败
func TestParseTokenTampered(t *testing.T) {
	token, err := GenerateToken(100, 200, "admin")
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}

	// 篡改 token：替换最后一个字符，破坏签名
	tampered := token[:len(token)-1] + "X"

	_, err = ParseToken(tampered)
	if err == nil {
		t.Errorf("篡改的 token 应解析失败，但没有返回错误")
	}
	t.Logf("篡改 token 解析失败(符合预期): %v", err)
}
