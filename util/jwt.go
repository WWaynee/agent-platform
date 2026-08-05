package util

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"agent-platform/config"
)

// Claims 自定义 JWT 载荷
type Claims struct {
	UserID   uint64 `json:"user_id"`
	TenantID uint64 `json:"tenant_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateToken 签发 JWT
// 载荷包含 user_id / tenant_id / username / role，过期时间取 config.JWT.ExpireSeconds
func GenerateToken(userID, tenantID uint64, username, role string) (string, error) {
	expire := time.Duration(config.GlobalConfig.JWT.ExpireSeconds) * time.Second

	claims := &Claims{
		UserID:   userID,
		TenantID: tenantID,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expire)), // 过期时间
			IssuedAt:  jwt.NewNumericDate(time.Now()),             // 签发时间
		},
	}

	// 用 HS256 签名
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.GlobalConfig.JWT.Secret))
}

// ParseToken 解析并校验 JWT，返回载荷 Claims
func ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (interface{}, error) {
			// 校验签名算法，防止算法混淆攻击
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
