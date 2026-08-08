package toolmanager

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"agent-platform/agent/interfaces"
)

// 假工具：仅用于测试管理器逻辑，不含真实业务。
type fakeTool struct{}

func (fakeTool) Name() string        { return "fake_tool" }
func (fakeTool) Description() string { return "测试用假工具" }
func (fakeTool) Parameters() string  { return `{"type":"object"}` }
func (fakeTool) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	return "executed:" + params + ":tenant=" + strconv.FormatUint(ctx.TenantID, 10), nil
}

func TestToolManager(t *testing.T) {
	m := NewToolManager()
	ft := fakeTool{}

	// 注册
	if err := m.RegisterTool(ft); err != nil {
		t.Fatalf("首次注册应成功，got err: %v", err)
	}

	// 重复注册应报错
	if err := m.RegisterTool(ft); err == nil {
		t.Fatal("重复注册应返回错误")
	}

	// 空名注册应报错
	if err := m.RegisterTool(toolWithName{}); err == nil {
		t.Fatal("空工具名注册应返回错误")
	}

	// 查找：命中
	if got, ok := m.GetTool("fake_tool"); !ok || got.Name() != "fake_tool" {
		t.Fatalf("应找到 fake_tool，ok=%v got=%v", ok, got)
	}
	// 查找：未命中
	if _, ok := m.GetTool("nope"); ok {
		t.Fatal("不应找到未注册的工具")
	}

	// 列出：应含且仅含已注册工具
	list := m.ListTools()
	if len(list) != 1 || list[0].Name() != "fake_tool" {
		t.Fatalf("ListTools 结果不符: %+v", list)
	}

	// 执行：命中
	out, err := m.ExecuteTool(interfaces.AgentContext{TenantID: 7}, "fake_tool", `{"q":"hi"}`)
	if err != nil {
		t.Fatalf("执行已注册工具应成功: %v", err)
	}
	if !strings.Contains(out, "tenant=7") {
		t.Fatalf("执行结果应透传租户上下文, got %q", out)
	}

	// 执行：未注册
	if _, err := m.ExecuteTool(interfaces.AgentContext{}, "nope", ""); err == nil {
		t.Fatal("执行未注册工具应返回错误")
	}
}

// 空名工具：Name 返回空串。
type toolWithName struct{}

func (toolWithName) Name() string        { return "" }
func (toolWithName) Description() string { return "" }
func (toolWithName) Parameters() string  { return "" }
func (toolWithName) Execute(ctx interfaces.AgentContext, params string) (string, error) {
	return "", nil
}

// 测试用权限检查器：只允许指定工具名的工具被调用。
type allowChecker struct {
	allowed map[string]bool
}

func (c *allowChecker) Check(ctx interfaces.AgentContext, toolName string) error {
	if !c.allowed[toolName] {
		return fmt.Errorf("该租户未被授权使用工具 %q", toolName)
	}
	return nil
}

func TestExecuteTool_Permission(t *testing.T) {
	m := NewToolManager()
	if err := m.RegisterTool(fakeTool{}); err != nil {
		t.Fatal(err)
	}

	// 场景1：未注入权限检查器 → 默认全部放行
	if _, err := m.ExecuteTool(interfaces.AgentContext{}, "fake_tool", `{}`); err != nil {
		t.Fatalf("未注入权限时放行失败: %v", err)
	}

	// 场景2：注入检查器，未授权工具 → 返回错误，且不执行
	m.SetPermissionChecker(&allowChecker{allowed: map[string]bool{}})
	if _, err := m.ExecuteTool(interfaces.AgentContext{}, "fake_tool", `{}`); err == nil {
		t.Fatal("未授权工具应返回权限错误")
	}

	// 场景3：注入检查器，授权工具 → 正常执行
	m.SetPermissionChecker(&allowChecker{allowed: map[string]bool{"fake_tool": true}})
	out, err := m.ExecuteTool(interfaces.AgentContext{TenantID: 3}, "fake_tool", `{"q":"hi"}`)
	if err != nil {
		t.Fatalf("授权工具应正常执行: %v", err)
	}
	if !strings.Contains(out, "tenant=3") {
		t.Fatalf("执行结果应透传租户上下文, got %q", out)
	}
}
