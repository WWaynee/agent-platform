package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ============ 常用错误码常量 ============

const (
	CodeSuccess      = 0   // 成功
	CodeBadRequest   = 400 // 参数错误
	CodeUnauthorized = 401 // 未登录
	CodeForbidden    = 403 // 无权限
	CodeServerError  = 500 // 服务器内部错误
)

// ============ 统一返回结构体 ============

// Body 统一响应结构体
// code: 0 成功，非 0 失败
// message: 提示信息
// data: 业务数据
type Body struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// ============ 工具函数 ============

// Success 成功返回，code=0，HTTP 状态码 200
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Body{
		Code:    CodeSuccess,
		Message: "ok",
		Data:    data,
	})
}

// SuccessMessage 成功返回，可自定义成功提示信息
func SuccessMessage(c *gin.Context, message string, data interface{}) {
	c.JSON(http.StatusOK, Body{
		Code:    CodeSuccess,
		Message: message,
		Data:    data,
	})
}

// Fail 失败返回，code 非 0，HTTP 状态码固定为 200（业务错误由 code 区分）
func Fail(c *gin.Context, code int, message string) {
	c.JSON(http.StatusOK, Body{
		Code:    code,
		Message: message,
		Data:    nil,
	})
}

// FailStatus 失败返回，支持自定义 HTTP 状态码与错误码
func FailStatus(c *gin.Context, httpStatus, code int, message string) {
	c.JSON(httpStatus, Body{
		Code:    code,
		Message: message,
		Data:    nil,
	})
}

// ============ 常用错误码便捷方法 ============

// BadRequest 400 参数错误
func BadRequest(c *gin.Context, message string) {
	Fail(c, CodeBadRequest, message)
}

// Unauthorized 401 未登录
func Unauthorized(c *gin.Context, message string) {
	Fail(c, CodeUnauthorized, message)
}

// Forbidden 403 无权限
func Forbidden(c *gin.Context, message string) {
	Fail(c, CodeForbidden, message)
}

// ServerError 500 服务器内部错误
func ServerError(c *gin.Context, message string) {
	Fail(c, CodeServerError, message)
}
