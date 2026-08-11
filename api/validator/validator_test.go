package validator

import (
	"errors"
	"testing"

	"github.com/go-playground/validator/v10"
)

// 验证：validator 依赖引入成功，Engine 能按 struct tag 校验，
//       校验错误能被翻译成 `字段名 → 中文提示` 的结构化错误。

type registerReq struct {
	TenantID uint64 `json:"tenant_id" binding:"required"`
	Username string `json:"username" binding:"required,min=2"`
	Password string `json:"password" binding:"required,min=6,max=64"`
	Role     string `json:"role" binding:"omitempty,oneof=admin member"`
}

// TestEngineValidate_StructTag 直接测 Engine.Struct：required/oneof 标签生效。
func TestEngineValidate_StructTag(t *testing.T) {
	// 缺 TenantID/Username/Password → 应校验失败
	if err := Engine.Struct(registerReq{}); err == nil {
		t.Fatal("缺必填字段应校验失败，但没有返回错误")
	}

	// 全字段填对 → 应通过
	ok := registerReq{TenantID: 1, Username: "zhangsan", Password: "secret123", Role: "member"}
	if err := Engine.Struct(ok); err != nil {
		t.Fatalf("合法数据应通过校验，实际报错: %v", err)
	}

	// Role 取值非法（既非 admin 也非 member）→ 应校验失败
	badRole := registerReq{TenantID: 1, Username: "ab", Password: "secret123", Role: "superuser"}
	if err := Engine.Struct(badRole); err == nil {
		t.Fatal("Role 取值非法应校验失败，但没有返回错误")
	}
}

// TestTranslate_StructuredFields 验证校验失败能被翻译成结构化字段错误，
// 且字段 key 用的是 json 标签名（username），而非 Go 字段名（Username）。
func TestTranslate_StructuredFields(t *testing.T) {
	err := Engine.Struct(registerReq{Username: "a", Password: "short", Role: "boss"})
	if err == nil {
		t.Fatal("应校验失败")
	}

	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("应为 validator.ValidationErrors，实际: %v", err)
	}

	fields := translate(verrs)
	if len(fields) == 0 {
		t.Fatal("translate 应返回非空字段错误映射")
	}
	// 字段 key 用 json 标签名
	if msg, ok := fields["username"]; !ok {
		t.Fatalf("应包含 username 字段错误，实际 keys: %#v", fields)
	} else if msg == "" {
		t.Fatal("username 错误提示不能为空")
	}
	if msg, ok := fields["password"]; !ok {
		t.Fatalf("应包含 password 字段错误，实际 keys: %#v", fields)
	} else if msg == "" {
		t.Fatal("password 错误提示不能为空")
	}
	// Role 非法取值 oneof 提示
	if msg, ok := fields["role"]; !ok {
		t.Fatalf("应包含 role 字段错误，实际 keys: %#v", fields)
	} else if msg == "" {
		t.Fatal("role 错误提示不能为空")
	}
}
