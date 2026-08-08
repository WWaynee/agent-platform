package toolmanager

import (
	"fmt"
	"sort"
	"sync"

	"agent-platform/agent/engine"
)

// ============ 工具管理器 ============

// ToolManager 是所有工具的注册中心与统一执行入口。
// 职责：插件化注册、按名查找、列出全部、统一执行。
//
// 为什么需要它：
//   - 插件化：新增工具只需实现 Tool 接口后 RegisterTool，无需改动引擎代码；
//   - 统一入口：所有工具调用都经过这里，后续可集中挂载日志、权限校验、限流；
//   - 符合开闭原则：对扩展开放（注册新工具），对修改关闭（不改引擎）。
//
// 并发安全：所有方法加互斥锁，允许多协程并发注册与调用。
type ToolManager struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewToolManager 构造一个空的工具管理器。
func NewToolManager() *ToolManager {
	return &ToolManager{
		tools: make(map[string]Tool),
	}
}

// RegisterTool 注册一个工具到管理器。
// 以工具名（tool.Name()）为 key 存入 map。
// 若同名工具已存在则返回错误，避免意外覆盖已注册工具。
func (m *ToolManager) RegisterTool(tool Tool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := tool.Name()
	if name == "" {
		return fmt.Errorf("注册工具失败：工具名不能为空")
	}
	if _, exists := m.tools[name]; exists {
		return fmt.Errorf("注册工具失败：工具 %q 已存在", name)
	}

	m.tools[name] = tool
	return nil
}

// GetTool 按工具名查找工具。
// 找到返回该工具与 true；找不到（或名字为空）返回 nil 与 false。
func (m *ToolManager) GetTool(name string) (Tool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tool, ok := m.tools[name]
	return tool, ok
}

// ListTools 按工具名排序返回当前所有已注册工具。
// 排序保证输出稳定，便于把工具列表稳定地拼进给 LLM 的 Prompt。
func (m *ToolManager) ListTools() []Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.tools) == 0 {
		return nil
	}

	// 按名字排序，保证多次列出顺序一致
	names := make([]string, 0, len(m.tools))
	for name := range m.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	list := make([]Tool, 0, len(names))
	for _, name := range names {
		list = append(list, m.tools[name])
	}
	return list
}

// ExecuteTool 统一执行指定工具。
// 先按名查找工具，找不到返回错误；找到后调用其 Execute 返回结果。
// 后续在这里可统一追加日志、权限校验、限流等横切逻辑。
func (m *ToolManager) ExecuteTool(ctx engine.AgentContext, name, params string) (string, error) {
	m.mu.RLock()
	tool, ok := m.tools[name]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("执行工具失败：未注册的工具 %q", name)
	}
	return tool.Execute(ctx, params)
}
