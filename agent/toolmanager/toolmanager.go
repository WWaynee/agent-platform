package toolmanager

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"agent-platform/agent/interfaces"
	"agent-platform/observability"

	"go.uber.org/zap"
)

// ============ 工具权限校验 ============

// PermissionChecker 校验"当前租户/用户是否有权限使用某个工具"。
//
// 引擎与 ToolManager 本体都无需改动，只需实现本接口并注入 ToolManager，
// ExecuteTool 执行前会自动调用 Check。
// 实际实现为 api/service.DBPermissionChecker（基于数据库 tenant_tool_config 白名单）。
type PermissionChecker interface {
	// Check 检查某租户是否有权限使用 toolName 工具。
	// 允许返回 nil；禁止返回非 nil 错误（会中断工具执行）。
	Check(ctx interfaces.AgentContext, toolName string) error
}

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
	// perm 权限检查器；nil 表示未启用权限校验（默认全部放行）。
	perm PermissionChecker
}

// NewToolManager 构造一个空的工具管理器。
func NewToolManager() *ToolManager {
	return &ToolManager{
		tools: make(map[string]Tool),
	}
}

// SetPermissionChecker 注入权限检查器。
// 可传 nil 来关闭权限校验（回到全部放行）。
func (m *ToolManager) SetPermissionChecker(pc PermissionChecker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.perm = pc
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
// 流程：① 按名查找，找不到返回错误 → ② 权限校验，无权限返回错误 →
//
//	③ 通过后调用其 Execute 返回结果。
//
// 后续可继续在这里统一追加日志、限流等横切逻辑。
func (m *ToolManager) ExecuteTool(ctx interfaces.AgentContext, name, params string) (string, error) {
	// ① 先按名查找工具（共享读写锁，读取阶段用 RLock）
	m.mu.RLock()
	tool, ok := m.tools[name]
	pc := m.perm
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("执行工具失败：未注册的工具 %q", name)
	}

	// ② 权限校验：执行前检查当前租户是否有权限使用该工具。
	//    注入的 checker 未启用（nil）时直接放行；启用后无权限 Check 返回错误即中断执行。
	//    实际接入的是 api/service.DBPermissionChecker（基于 tenant_tool_config 白名单）。
	if pc != nil {
		if err := pc.Check(ctx, name); err != nil {
			return "", fmt.Errorf("执行工具失败：工具 %q 无权限：%w", name, err)
		}
	}

	// ③ 通过校验，执行工具
	logger := observability.WithTenantUser(ctx.TenantID, ctx.UserID)
	start := time.Now()

	// 调用工具前：记录 tool_name / params
	// ⚠️ params 来自 LLM 生成，可能与 LLM 输入产生重叠，这里只记录（工具执行场景需要，便于排查）；
	//    若个别工具参数含敏感信息，应在工具自身的日志处理里规避。
	logger.Info("调用工具",
		zap.String("tool_name", name),
		zap.String("params", params),
	)

	result, err := tool.Execute(ctx, params)

	// 调用工具后：记录耗时、是否成功
	latency := time.Since(start).Milliseconds()
	if err != nil {
		logger.Error("工具执行失败",
			zap.Error(err),
			zap.String("tool_name", name),
			zap.Int64(observability.FieldLatency, latency),
		)
		return "", err
	}
	logger.Info("工具执行成功",
		zap.String("tool_name", name),
		zap.Int64(observability.FieldLatency, latency),
	)
	return result, nil
}
