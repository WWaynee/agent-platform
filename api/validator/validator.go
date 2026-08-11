package validator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"agent-platform/api/response"
)

// ============ 统一参数校验（基于 go-playground/validator/v10） ============
//
// 目标：所有 handler 的请求参数统一在这里绑定 + 校验 + 生成错误响应，
//       不再各自手写 if 判断。
// 依赖：github.com/go-playground/validator/v10（Go 生态最常用的结构体标签校验库，
//        Gin 内置 binding 也是基于它；这里创建独立实例便于统一注册自定义校验规则）。

// Engine 全局校验引擎实例。
// 必须用 validator.New() 创建单例，而非零值结构体，才能正确解析 struct tag 的校验规则。
var Engine = validator.New()

func init() {
	// 解析结构体上的 `binding:"..."` 标签（与 gin.ShouldBindJSON 标签风格对齐）。
	Engine.SetTagName("binding")

	// 让校验错误消息里的字段名用 `json`/`form` 标签名（如 Username → username），
	// 而不是 Go 字段名，这样返回给前端的字段 key 与请求参数键完全一致。
	Engine.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			name = strings.SplitN(fld.Tag.Get("form"), ",", 2)[0]
		}
		return name
	})
}

// ValidationError 结构化校验错误。
// Fields 为 `字段名(json/form键) → 中文错误原因`，供前端定位具体字段。
type ValidationError struct {
	Fields map[string]string
}

// Error 实现 error 接口（msg 暂时汇总字段名，便于日志/调试）。
func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	return fmt.Sprintf("参数校验失败，字段: %v", keys)
}

// BindJSON 绑定并校验 JSON 请求体。
//
// 流程：① 标准库 json.Unmarshal 只做反序列化（不触发 Gin 内置校验）
//       ② 用本项目统一的 Engine.Struct 做标签校验（validator/v10）
//
// 校验失败返回 *ValidationError（含每个字段的中文错误原因），
// 其余异常（空体 / JSON 格式错 / 读流失败）返回普通 error。
// handler 统一用 HandleBindError 转写响应。
func BindJSON(c *gin.Context, obj any) error {
	if c.Request == nil || c.Request.Body == nil {
		return errors.New("缺少请求体")
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("请求体不能为空")
	}

	// ① 反序列化（仅 JSON 解析，不在此做校验）
	if err := json.Unmarshal(body, obj); err != nil {
		return err
	}

	// ② 统一的 struct 标签校验
	if err := Engine.Struct(obj); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			return &ValidationError{Fields: translate(verrs)}
		}
		return err
	}
	return nil
}

// translate 把 validator.ValidationErrors 翻译成 `字段名 → 中文提示`。
func translate(verrs validator.ValidationErrors) map[string]string {
	fields := make(map[string]string, len(verrs))
	for _, fe := range verrs {
		name := fe.Field()
		if name == "" {
			name = fe.StructField()
		}
		fields[name] = fieldErrorMessage(fe)
	}
	return fields
}

// fieldErrorMessage 单个字段错误的中文提示。
// 区分字符串（按字符长度）与数值（按数值大小）；标签不同给出不同文案。
func fieldErrorMessage(fe validator.FieldError) string {
	isString := fe.Kind() == reflect.String || fe.Kind() == reflect.Slice || fe.Kind() == reflect.Map
	switch fe.Tag() {
	case "required":
		return "该字段为必填项，不能为空"
	case "email":
		return "邮箱格式不正确"
	case "oneof":
		return fmt.Sprintf("取值只能为：%s", fe.Param())
	case "min":
		if isString {
			return fmt.Sprintf("长度不能少于 %s 个字符", fe.Param())
		}
		return fmt.Sprintf("数值不能小于 %s", fe.Param())
	case "max":
		if isString {
			return fmt.Sprintf("长度不能超过 %s 个字符", fe.Param())
		}
		return fmt.Sprintf("数值不能大于 %s", fe.Param())
	default:
		return "参数不合法"
	}
}

// HandleBindError 绑定/校验失败的统一响应出口。
//   - *ValidationError → 400，data = {字段: 中文错误}，前端可精确定位
//   - 其他错误（空体 / JSON 格式错 / 读流失败）→ 400 通用提示
//
// handler 统一这样写：
//
//	if err := validator.BindJSON(c, &req); err != nil {
//		validator.HandleBindError(c, err)
//		return
//	}
func HandleBindError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		response.BadRequestValidation(c, ve.Fields)
		return
	}
	response.BadRequest(c, "参数校验失败: "+err.Error())
}
