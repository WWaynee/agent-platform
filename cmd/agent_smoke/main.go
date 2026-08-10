// Command agent_smoke 用于验证 Agent 骨架各组件能协同工作（冒烟测试）。
//
// 不真正调用 LLM（用 mock），也不真正 Run 主循环，只验证：
//  1. 所有组件能正常创建
//  2. 工具能注册、能找到
//  3. 记忆能存能取
//  4. 整个依赖链（Interfaces/ToolManager/Memory/Engine）是通的
package main

import (
	"context"
	"fmt"

	"agent-platform/agent/engine"
	"agent-platform/agent/memory"
	"agent-platform/agent/toolmanager"
	"agent-platform/toolkit"
)

// mockLLM 模拟 LLM 客户端：骨架阶段不真调大模型，只声明实现了接口。
type mockLLM struct{}

func (mockLLM) Chat(ctx context.Context, req engine.ChatRequest) (string, error) {
	return "", fmt.Errorf("骨架阶段不真正调用 LLM（mock）")
}

func main() {
	// 1. 创建各组件
	llm := mockLLM{}                   // LLM 客户端（mock）
	tm := toolmanager.NewToolManager() // 工具管理器
	mem := memory.NewInMemoryMemory()  // 记忆管理器（内存版）

	// 2. 注册 echo 测试工具
	if err := tm.RegisterTool(toolkit.NewEchoTool()); err != nil {
		fmt.Printf("❌ 注册工具失败: %v\n", err)
		return
	}

	// 3. 创建 ReActEngine，把三组件传进去
	e := engine.NewReActEngine(llm, tm, mem, "你是一个测试助手")
	ctx := engine.AgentContext{TenantID: 7, SessionID: "smoke-session"}

	ok := true

	// ① 工具能注册、能找到
	if got, found := tm.GetTool("echo"); !found {
		fmt.Println("❌ 找不到 echo 工具")
		ok = false
	} else {
		fmt.Printf("✅ 工具 echo 已注册并找到（描述: %s）\n", got.Description())
	}

	// ② 记忆能存能取（带租户隔离：tenantID=ctx.TenantID）
	mem.AddMessage(ctx.TenantID, "s1", memory.ChatMessage{Role: memory.RoleUser, Content: "你好"})
	hist := mem.GetHistory(ctx.TenantID, "s1")
	if len(hist) != 1 || hist[0].Content != "你好" {
		fmt.Println("❌ 记忆存/取失败")
		ok = false
	} else {
		fmt.Println("✅ 记忆能存能取")
	}

	// ③ 引擎三组件已注入
	if e.LLMClient == nil || e.ToolManager == nil || e.Memory == nil {
		fmt.Println("❌ 引擎三组件注入不完整")
		ok = false
	} else {
		fmt.Printf("✅ 引擎创建成功，三组件就绪（MaxIterations=%d）\n", e.MaxIterations)
	}

	// ④ 工具统一执行入口可用（echo 直接回显参数）
	out, err := tm.ExecuteTool(ctx, "echo", "hello-world")
	if err != nil || out != "hello-world" {
		fmt.Printf("❌ 执行 echo 工具失败: out=%q err=%v\n", out, err)
		ok = false
	} else {
		fmt.Println("✅ echo 工具执行链路通（回显: hello-world）")
	}

	if ok {
		fmt.Println("\n🎉 骨架全部打通：Interfaces / ToolManager / Memory / Engine 协同正常")
	}
}
