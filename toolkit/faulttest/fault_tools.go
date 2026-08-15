// Package faulttest 提供一组仅供「工具执行失败容错」测试/联调使用的故障工具。
//
// 用途：验证 ReAct 引擎在多类工具故障下仍能容错运行——不 panic、
// 有超时控制、把工具错误喂回给 LLM 让其决定下一步。
// 不属于生产能力，仅供自测与 e2e 联调。
package faulttest

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agent-platform/agent/interfaces"
)

// ============ sleep_tool：模拟耗时很久的工具（验证工具执行超时） ============

// SleepTool 模拟一个可能阻塞很久的耗时工具。
// 它从 AgentContext 派生标准 ctx，并 select 监听 ctx.Done()：
// 当引擎侧给工具定了超时 deadline 时，超时后工具会感知并中止返回，而不是无限 sleep。
type SleepTool struct{}

// NewSleepTool 构造慢工具。
func NewSleepTool() *SleepTool { return &SleepTool{} }

// Name 返回工具唯一标识。
func (SleepTool) Name() string { return "sleep_tool" }

// Description 返回工具描述。
func (SleepTool) Description() string {
	return "模拟耗时很长的慢工具：会长时间阻塞，仅用于测试工具超时控制是否生效。"
}

// Parameters 返回参数说明。
func (SleepTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"duration_ms": {
				"type": "integer",
				"description": "模拟耗时的毫秒数（默认很大，用于触发超时）"
			}
		},
		"required": []
	}`
}

// Execute 执行慢工具：阻塞到 ctx 取消（超时）为止。
// ⚠️ 关键示范：使用可取消的标准 ctx（ctx.ToContext）——引擎在工具超时时会取消它，
// 工具据此 select 退出，从而"不会无限等"。
func (SleepTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	// 把 AgentContext 译成可感知 deadline/cancellation 的标准 ctx。
	stdCtx := ctx.ToContext(nil)

	// 模拟一个超长时间的操作；一旦 ctx 被取消/超时立即返回。
	select {
	case <-stdCtx.Done():
		if err := context.Cause(stdCtx); err != nil {
			return "", fmt.Errorf("工具执行超时或已取消: %w", err)
		}
		return "", fmt.Errorf("工具执行被取消")
	case <-time.After(24 * time.Hour):
		return "意外：本工具不应真正跑完 24 小时", nil
	}
}

// ============ fail_tool：直接返回错误的工具（验证工具执行报错） ============

// FailTool 每次执行都返回一个明确的业务错误。
// 引擎应把该错误作为观察结果喂给 LLM，让 LLM 决定下下一步，且全程不 panic。
type FailTool struct{}

// NewFailTool 构造报错工具。
func NewFailTool() *FailTool { return &FailTool{} }

// Name 返回工具唯一标识。
func (FailTool) Name() string { return "fail_tool" }

// Description 返回工具描述。
func (FailTool) Description() string {
	return "演示工具：无论怎么调用都会返回一个错误，用于测试工具执行失败的处理。"
}

// Parameters 返回参数说明。
func (FailTool) Parameters() string { return `{"type":"object"}` }

// Execute 直接返回错误。
func (FailTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	return "", fmt.Errorf("fail_tool 模拟的业务故障")
}

// ============ param_tool：要求严格参数的工具（验证参数错误处理） ============

// ParamTool 对传入参数做严格校验：要求是含必填字段 text 的合法 JSON 对象。
// 若 LLM 传参格式不对（非 JSON / 缺字段），返回参数错误；
// 引擎应把"参数错误"喂回给 LLM，让 LLM 修正后重试。
type ParamTool struct{}

// NewParamTool 构造参数校验工具。
func NewParamTool() *ParamTool { return &ParamTool{} }

// Name 返回工具唯一标识。
func (ParamTool) Name() string { return "param_tool" }

// Description 返回工具描述。
func (ParamTool) Description() string {
	return "演示参数校验：必须传入合法的 JSON：{\"text\": \"...\"}，text 为必填非空字符串。" +
		"若参数不是这种结构，工具会返回『参数错误』提示你修正。"
}

// Parameters 返回参数说明。
func (ParamTool) Parameters() string {
	return `{
		"type": "object",
		"properties": {
			"text": {"type": "string", "description": "必填，非空字符串"}
		},
		"required": ["text"]
	}`
}

// Execute 校验参数并返回。
func (ParamTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	if len(params) == 0 || params[0] != '{' {
		return "", fmt.Errorf("param_tool 参数错误：需传合法 JSON 对象，且必填字段 text（非空字符串）")
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(params), &req); err != nil {
		return "", fmt.Errorf("param_tool 参数错误：%v", err)
	}
	if req.Text == "" {
		return "", fmt.Errorf("param_tool 参数错误：字段 text 不能为空")
	}
	return "param_tool 收到: " + req.Text, nil
}
