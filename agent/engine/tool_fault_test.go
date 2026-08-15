package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"agent-platform/agent/interfaces"
	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
	"agent-platform/toolkit/faulttest"
)

// ============ 工具执行失败容错测试 ============
//
// 覆盖 5 类工具故障，自测点：
//   1. 工具不存在        → 引擎返回"工具不存在"，不崩溃，LLM 可继续尝试
//   2. 参数错误          → 有处理，错误喂回 LLM
//   3. 工具执行超时      → 有超时控制，不会无限等
//   4. 工具执行报错      → 错误信息返回给 LLM
//   5. 工具未启用        → 返回"工具未启用"，不崩溃
//
// 全程服务不 panic、引擎能处理错误、有降级。

// errToolForbidden 模拟工具被禁用的错误。
var errToolForbidden = errors.New("该工具未启用（无权限）")

// denyChecker 权限校验器：拒绝所有工具，模拟"工具未启用"。
// 实现 toolmanager.PermissionChecker 接口。
type denyChecker struct{}

// Check 总是返回"未启用"错误（无权限）。
func (denyChecker) Check(ctx interfaces.AgentContext, toolName string) error {
	return errToolForbidden
}

// ================================================================
// 1. 工具不存在：LLM 调用未注册的工具 → 不崩溃、错误喂回 LLM、可继续
// ================================================================
func TestToolFault_ToolNotExist(t *testing.T) {
	// 第1轮 LLM 调用不存在的工具 "ghost_tool"，第2轮 final_answer（说明 LLM 能继续）
	llm := &captureLLM{
		replies: []string{
			`{"action":"ghost_tool","action_input":"x"}`,
			`{"action":"final_answer","action_input":"该工具不存在，我改用其他方式回答"}`,
		},
	}
	tm := toolmanager.NewToolManager() // 不注册 ghost_tool
	e := NewReActEngine(llm, tm, memory.NewInMemoryMemory(), "你是一个助手")

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "tool-not-exist"}, "调用不存在的工具")
	})
	if err != nil {
		t.Fatalf("工具不存在不应返回错误: %v", err)
	}
	if resp.Answer != "该工具不存在，我改用其他方式回答" {
		t.Fatalf("LLM 应能继续并给出最终回答, got %q", resp.Answer)
	}
	second := llm.messagesJoined(1)
	if !strings.Contains(second, "不存在") && !strings.Contains(second, "未注册") {
		t.Fatalf("工具不存在错误应喂回 LLM, got: %s", second)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ToolName != "ghost_tool" {
		t.Fatalf("应记录一次 ghost_tool 调用, got %+v", resp.ToolCalls)
	}
}

// ================================================================
// 2. 参数错误：工具校验参数失败 → 错误喂回 LLM，LLM 修正后再成
// ================================================================
func TestToolFault_BadParamsFedBack(t *testing.T) {
	llm := &captureLLM{
		replies: []string{
			`{"action":"param_tool","action_input":"不是JSON"}`,
			`{"action":"final_answer","action_input":"我修正了参数"}`,
		},
	}
	tm := toolmanager.NewToolManager()
	_ = tm.RegisterTool(faulttest.NewParamTool())
	e := NewReActEngine(llm, tm, memory.NewInMemoryMemory(), "你是一个助手")

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "tool-bad-param"}, "调用带参数错误的工具")
	})
	if err != nil {
		t.Fatalf("参数错误不应返回引擎错误: %v", err)
	}
	if resp.Answer != "我修正了参数" {
		t.Fatalf("LLM 应能修正并回答, got %q", resp.Answer)
	}
	second := llm.messagesJoined(1)
	if !strings.Contains(second, "参数错误") {
		t.Fatalf("参数错误信息应喂回 LLM, got: %s", second)
	}
}

// ================================================================
// 3. 工具执行超时：sleep_tool 遇到超时应中止，不无限等待
// ================================================================
func TestToolFault_ToolTimeout(t *testing.T) {
	llm := &captureLLM{
		replies: []string{
			`{"action":"sleep_tool","action_input":"{}"}`,
			`{"action":"final_answer","action_input":"工具超时了，我及时收住"}`,
		},
	}
	tm := toolmanager.NewToolManager()
	_ = tm.RegisterTool(faulttest.NewSleepTool())
	e := NewReActEngine(llm, tm, memory.NewInMemoryMemory(), "你是一个助手")
	// 给工具设置一个很短的超时（100ms），验证"不会无限等"（sleep_tool 本应挂 24h）
	e.ToolTimeout = 100 * time.Millisecond

	start := time.Now()
	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "tool-timeout"}, "会超时的工具")
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("工具超时不应返回引擎错误: %v", err)
	}
	// 关键自测点：不会无限等——整轮 Run 应远小于 sleep 工具的 24h
	if elapsed > 5*time.Second {
		t.Fatalf("工具超时时整轮 Run 不应久等, 实际耗时 %v", elapsed)
	}
	if resp.Answer != "工具超时了，我及时收住" {
		t.Fatalf("LLM 应能感知超时并继续, got %q", resp.Answer)
	}
	second := llm.messagesJoined(1)
	if !strings.Contains(second, "超时") && !strings.Contains(second, "取消") {
		t.Fatalf("超时信息应喂回 LLM, got: %s", second)
	}
}

// ================================================================
// 4. 工具执行报错：fail_tool → 错误返回给 LLM，LLM 决定下一步
// ================================================================
func TestToolFault_ToolExecutionError(t *testing.T) {
	llm := &captureLLM{
		replies: []string{
			`{"action":"fail_tool","action_input":"x"}`,
			`{"action":"final_answer","action_input":"知道了工具出错，我已处理"}`,
		},
	}
	tm := toolmanager.NewToolManager()
	_ = tm.RegisterTool(faulttest.NewFailTool())
	e := NewReActEngine(llm, tm, memory.NewInMemoryMemory(), "你是一个助手")

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "tool-err"}, "触发工具报错")
	})
	if err != nil {
		t.Fatalf("工具报错不应返回引擎错误: %v", err)
	}
	if resp.Answer != "知道了工具出错，我已处理" {
		t.Fatalf("LLM 应能根据工具错误决定下一步, got %q", resp.Answer)
	}
	second := llm.messagesJoined(1)
	if !strings.Contains(second, "失败") && !strings.Contains(second, "错误") {
		t.Fatalf("工具错误应喂回 LLM, got: %s", second)
	}
}

// ================================================================
// 5. 工具未启用：权限校验拒绝（无权限）→ 返回"未启用"，不崩溃
// ================================================================
func TestToolFault_ToolDisabled(t *testing.T) {
	llm := &captureLLM{
		replies: []string{
			`{"action":"param_tool","action_input":"{\"text\":\"x\"}"}`,
			`{"action":"final_answer","action_input":"工具未启用，我改用别的回答"}`,
		},
	}
	tm := toolmanager.NewToolManager()
	_ = tm.RegisterTool(faulttest.NewParamTool())
	tm.SetPermissionChecker(denyChecker{}) // 注入权限校验器：拒绝所有工具
	e := NewReActEngine(llm, tm, memory.NewInMemoryMemory(), "你是一个助手")

	resp, err := mustNotPanic(t, func() (*AgentResponse, error) {
		return e.Run(AgentContext{TenantID: 1, SessionID: "tool-disabled"}, "用未启用的工具")
	})
	if err != nil {
		t.Fatalf("工具未启用不应返回引擎错误: %v", err)
	}
	if resp.Answer != "工具未启用，我改用别的回答" {
		t.Fatalf("LLM 应能感知未启用并继续, got %q", resp.Answer)
	}
	second := llm.messagesJoined(1)
	if !strings.Contains(second, "未启用") && !strings.Contains(second, "无权限") {
		t.Fatalf("工具未启用信息应喂回 LLM, got: %s", second)
	}
}
