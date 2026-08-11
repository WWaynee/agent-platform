package service

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"agent-platform/agent/interfaces"
	"agent-platform/agent/toolmanager"
	"agent-platform/storage"
)

// ============ 工具权限校验：基于 tenant_tool_config 白名单 ============
//
// 实现 toolmanager.PermissionChecker 接口：在 ToolManager.ExecuteTool 执行前，
// 查询当前租户是否开启了目标工具（tenant_tool_config 表）。没开则返回错误，中断工具执行。
// 这补上了 toolmanager.go 里预留的"权限管控" TODO 位。
//
// 依赖方向：本文件在业务层(service)，依赖 toolmanager 的接口（拿到插口）与 storage（查库），
// 无环。把具体校验逻辑下沉到业务层，agent/toolmanager 只保留抽象的 PermissionChecker 接口。

// DBPermissionChecker 基于数据库 tenant_tool_config 表的权限检查器。
type DBPermissionChecker struct{}

// NewDBPermissionChecker 构造一个基于 DB 白名单的权限检查器。
func NewDBPermissionChecker() toolmanager.PermissionChecker {
	return &DBPermissionChecker{}
}

// Check 检查当前租户是否有权限使用指定工具。
//   - 参数：ctx 提供 TenantID；toolName 为目标工具名。
//   - 返回 nil 表示有权限（放行）；返回错误表示无权限（中断执行）。
//
// 规则：
//  1. 工具名为空 → 拒绝（没有明确的工具就别放行）。
//  2. 先查 tenant_tool_config：查到且 IsEnable=false → 明确关闭，拒绝。
//  3. 查不到配置（历史租户未初始化 / 未做配置）→ **默认放行**：
//     - 数据库没有该租户对该工具的启用记录时，视为默认启用，放行。
//     - 这样既能兼容老租户未初始化配置，也避免"新增工具被老租户误拦截"。
func (DBPermissionChecker) Check(ctx interfaces.AgentContext, toolName string) error {
	if toolName == "" {
		return fmt.Errorf("工具名为空，无权限")
	}

	cfg, err := storage.GetToolConfig(ctx.TenantID, toolName)
	if err == nil {
		// 有明确配置记录
		if !cfg.IsEnable {
			return fmt.Errorf("该工具未启用，请联系管理员")
		}
		return nil // 已显式开启
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		// 查询出现其他错误：保守起见视为无权限（避免因 DB 异常放行）
		return fmt.Errorf("校验工具 %q 权限失败: %w", toolName, err)
	}

	// 查不到配置记录 → 默认放行（兼容老租户 / 未配置的新工具）
	return nil
}
