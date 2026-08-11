package handler

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"agent-platform/agent/toolmanager"
	"agent-platform/api/middleware"
	"agent-platform/api/response"
	"agent-platform/api/service"
	"agent-platform/api/validator"
)

// ============ 工具开关配置（租户管理员） ============
//
// GET /api/admin/tool-config          → 获取当前租户所有工具的开关状态
// PUT /api/admin/tool-config/:tool_name → 开关某个工具
//
// 说明：
//   - 租户管理员据此在管理端控制本租户可用哪些 Agent 工具（租户级工具权限隔离）。
//   - tenant_id 一律从 JWT 上下文拿（唯一可信来源），据此做多租户隔离。
//   - 仅管理员（role=admin）可调：路由组已挂 AdminAuth 中间件统一拦截（见 router.go）。

// toolMgr 工具管理器实例。由程序启动时（cmd/api/main.go）注入。
var toolMgr *toolmanager.ToolManager

// SetToolManager 注入工具管理器实例（启动时调用，只注入一次）。
// 用于遍历"已注册的全部工具"及其描述，构建开关列表。
func SetToolManager(m *toolmanager.ToolManager) {
	toolMgr = m
}

// UpdateToolConfigReq 开关工具请求
type UpdateToolConfigReq struct {
	IsEnable bool `json:"is_enable"` // true 启用 / false 禁用
}

// GetToolConfigList 获取当前租户所有工具的开关状态
// GET /api/admin/tool-config
// 返回：list = [{tool_name, is_enable, description}, ...]
func GetToolConfigList(c *gin.Context) {
	if toolMgr == nil {
		response.ServerError(c, "工具管理器未初始化")
		return
	}

	tenantID := middleware.GetTenantID(c)

	tools := toolMgr.ListTools()
	list := make([]gin.H, 0, len(tools))
	for _, t := range tools {
		enabled, err := service.GetToolEnabled(tenantID, t.Name())
		if err != nil {
			response.ServerError(c, "查询工具配置失败: "+err.Error())
			return
		}
		list = append(list, gin.H{
			"tool_name":   t.Name(),
			"is_enable":   enabled,
			"description": t.Description(),
		})
	}

	response.Success(c, gin.H{"list": list})
}

// UpdateToolConfig 开关某个工具
// PUT /api/admin/tool-config/:tool_name
// 入参：{"is_enable": true|false}
func UpdateToolConfig(c *gin.Context) {
	toolName := c.Param("tool_name")
	if toolName == "" {
		response.BadRequest(c, "tool_name 不能为空")
		return
	}

	// 仅允许配置"已注册"的工具，拒绝配置未注册/不存在的工具（防止写垃圾配置）
	if toolMgr == nil {
		response.ServerError(c, "工具管理器未初始化")
		return
	}
	if _, ok := toolMgr.GetTool(toolName); !ok {
		response.BadRequest(c, fmt.Sprintf("工具 %q 不存在或未注册", toolName))
		return
	}

	var req UpdateToolConfigReq
	if err := validator.BindJSON(c, &req); err != nil {
		validator.HandleBindError(c, err)
		return
	}

	tenantID := middleware.GetTenantID(c)
	if err := service.UpdateToolEnabled(tenantID, toolName, req.IsEnable); err != nil {
		response.ServerError(c, err.Error())
		return
	}

	response.Success(c, nil)
}
