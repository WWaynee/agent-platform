package util

import "golang.org/x/crypto/bcrypt"

// HashPassword 明文密码转 bcrypt 哈希
// bcrypt 自带随机盐，因此同一明文两次哈希结果不同
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword 校验明文密码与哈希是否匹配
// 匹配返回 true，不匹配返回 false
func VerifyPassword(plain, hashed string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain))
	return err == nil
}
