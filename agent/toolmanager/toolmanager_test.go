package toolmanager

import (
	"strconv"
	"strings"
	"testing"

	"agent-platform/agent/engine"
)

// 假工具：仅用于测试管理器逻辑，不含真实业务。
type fakeTool struct{}

func (fakeTool) Name() string        { return "fake_tool" }
func (fakeTool) Description() string { return "测试用假工具" }
func (fakeTool) Parameters() string  { return `{"type":"object"}` }
func (fakeTool) Execute(ctx engine.AgentContext, params string) (string, error) {
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
	out, err := m.ExecuteTool(engine.AgentContext{TenantID: 7}, "fake_tool", `{"q":"hi"}`)
	if err != nil {
		t.Fatalf("执行已注册工具应成功: %v", err)
	}
	if !strings.Contains(out, "tenant=7") {
		t.Fatalf("执行结果应透传租户上下文, got %q", out)
	}

	// 执行：未注册
	if _, err := m.ExecuteTool(engine.AgentContext{}, "nope", ""); err == nil {
		t.Fatal("执行未注册工具应返回错误")
	}
}

// 空名工具：Name 返回空串。
type toolWithName struct{}

func (toolWithName) Name() string        { return "" }
func (toolWithName) Description() string { return "" }
func (toolWithName) Parameters() string  { return "" }
func (toolWithName) Execute(ctx engine.AgentContext, params string) (string, error) {
	return "", nil
}
