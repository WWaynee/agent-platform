package util

import "testing"

// 验证：同一个明文密码，两次哈希结果不一样（因为随机盐）
func TestHashPasswordRandomSalt(t *testing.T) {
	plain := "admin123456"

	h1, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("第一次哈希失败: %v", err)
	}
	h2, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("第二次哈希失败: %v", err)
	}

	t.Logf("hash1: %s", h1)
	t.Logf("hash2: %s", h2)

	if h1 == h2 {
		t.Errorf("同一明文两次哈希结果应不同（随机盐），但得到了相同结果")
	}
}

// 验证：明文密码和哈希能验证通过
func TestVerifyPasswordMatch(t *testing.T) {
	plain := "admin123456"
	hash, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}

	if !VerifyPassword(plain, hash) {
		t.Errorf("正确密码应验证通过")
	}
}

// 验证：错误密码验证不通过
func TestVerifyPasswordWrong(t *testing.T) {
	plain := "admin123456"
	wrong := "wrong-password"

	hash, err := HashPassword(plain)
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}

	if VerifyPassword(wrong, hash) {
		t.Errorf("错误密码不应验证通过")
	}
}
