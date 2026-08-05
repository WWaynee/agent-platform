package util

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"agent-platform/config"
)

// Claims JWT 载荷（JWT 只放最小鉴权信息，不存放可变/展示型字段）
// UserID / TenantID / Role 是后续鉴权与权限判断要用到的核心标识
type Claims struct {
	UserID   uint64 `json:"user_id"`
	TenantID uint64 `json:"tenant_id"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateToken 签发 JWT
// 载荷包含 user_id / tenant_id / role，过期时间取 config.JWT.ExpireSeconds
// 多租户安全：tenant_id 从 token 获取，前端不可伪造
func GenerateToken(userID, tenantID uint64, role string) (string, error) {
	expire := time.Duration(config.GlobalConfig.JWT.ExpireSeconds) * time.Second

	claims := &Claims{
		UserID:   userID,
		TenantID: tenantID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expire)), // 过期时间
			IssuedAt:  jwt.NewNumericDate(time.Now()),             // 签发时间
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GlobalConfig.JWT.Secret))
}

// ParseToken 解析并校验 JWT，返回 Claims
// 校验：签名是否正确、签名算法是否为 HS256、是否过期
func ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (interface{}, error) {
			// 校验签名算法，防止算法混淆攻击（必须为 HMAC 系列）
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("签名算法不匹配")
			}
			return []byte(config.GlobalConfig.JWT.Secret), nil
		},
	)
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("无效的 token")
	}
	return claims, nil
}
